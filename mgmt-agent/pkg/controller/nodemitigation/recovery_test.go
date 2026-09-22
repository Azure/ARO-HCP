// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nodemitigation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func TestMultipleStalledPodsDoNotRequireAllReplicasReady(t *testing.T) {
	f := swiftFixture(t)
	pod, err := f.kube.CoreV1().Pods("test").Get(context.Background(), "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod.Name, pod.UID, pod.Spec.NodeName = "router-stalled-2", "second", "node-02"
	if err := f.kube.Tracker().Add(pod); err != nil {
		t.Fatal(err)
	}
	event, err := f.kube.CoreV1().Events("test").Get(context.Background(), "sandbox", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	event.Name, event.Source.Host = "sandbox-2", pod.Spec.NodeName
	event.InvolvedObject.Name, event.InvolvedObject.UID = pod.Name, pod.UID
	if err := f.kube.Tracker().Add(event); err != nil {
		t.Fatal(err)
	}
	deployment, err := f.kube.AppsV1().Deployments("test").Get(context.Background(), "router", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deployment.Spec.Replicas = ptr.To(int32(3))
	deployment.Status.Replicas, deployment.Status.UpdatedReplicas = 3, 3
	if err := f.kube.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), deployment, "test"); err != nil {
		t.Fatal(err)
	}
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		meta := action.(ktesting.CreateAction).GetObject().(metav1.Object)
		err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", meta.GetName())
		return true, nil, err
	})
	f.tick(t)
	f.now = f.now.Add(f.cfg.EvictionCooldown.Duration)
	f.tick(t)
	if evictionCount(f.kube.Actions()) != 2 || len(f.ledger(t).Status.Evictions) != 2 {
		t.Fatal("rescue waited for a replacement checkpoint or all replicas")
	}
}

func TestLostLedgerWithOwnershipCannotResetHistory(t *testing.T) {
	for _, emptyLedger := range []bool{false, true} {
		for _, mode := range []Mode{Audit, Enforce} {
			t.Run(fmt.Sprintf("empty=%v/%s", emptyLedger, mode), func(t *testing.T) {
				f := swiftFixture(t)
				f.tick(t)
				budgets := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent")
				if err := budgets.Delete(context.Background(), budgetName, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
				if emptyLedger {
					_, err := budgets.Create(context.Background(), &api.NodeMitigationBudget{
						ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
					}, metav1.CreateOptions{})
					if err != nil {
						t.Fatal(err)
					}
				}
				f.cfg.Mode = mode
				if err := f.controller.SetConfig(f.cfg); err != nil {
					t.Fatal(err)
				}
				f.kube.ClearActions()
				f.records.ClearActions()
				err := f.controller.reconcile(context.Background())
				if err == nil || !strings.Contains(err.Error(), "without initialized accounting") {
					t.Fatalf("lost accounting not reported: %v", err)
				}
				if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
					t.Fatal("lost accounting allowed new writes")
				}
			})
		}
	}
}

func TestMitigationEventsUseTargetNamespace(t *testing.T) {
	f := swiftFixture(t)
	f.tick(t)
	events, err := f.kube.CoreV1().Events("test").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events.Items {
		if event.Reason == "PodEvictionAccepted" && event.Namespace == event.InvolvedObject.Namespace {
			return
		}
	}
	t.Fatal("eviction Event missing from pod namespace")
}

func TestUnsupportedLedgerCannotResetHistory(t *testing.T) {
	f := swiftFixture(t)
	b := &api.NodeMitigationBudget{ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
		Status: api.NodeMitigationBudgetStatus{Evictions: map[string]api.EvictionRecord{"existing": {NodeUID: "existing"}}}}
	if err := f.records.Tracker().Add(b); err != nil {
		t.Fatal(err)
	}
	f.records.ClearActions()
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("unsupported history silently migrated")
	}
	if len(mutations(f.records.Actions())) != 0 || len(mutations(f.kube.Actions())) != 0 {
		t.Fatal("unsupported ledger changed")
	}
}
