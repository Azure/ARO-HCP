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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func TestLostOperationPersistenceDoesNotReplay(t *testing.T) {
	f := newFixture(t, 11, 1)
	f.tick(t)
	f.records.PrependReactor("update", "nodemitigationbudgets", func(action ktesting.Action) (bool, runtime.Object, error) {
		budget := action.(ktesting.UpdateAction).GetObject().(*api.NodeMitigationBudget)
		for _, r := range budget.Status.Reservations {
			if r.OperationToken != "" {
				return true, nil, apierrors.NewConflict(api.Resource("nodemitigationbudgets"), budgetName, errors.New("lost status update"))
			}
		}
		return false, nil, nil
	})
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("status persistence failure hidden")
	}
	f.controller.observer = "new-leader"
	f.controller.nextPoll = map[string]time.Time{}
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("unknown outcome was not reported")
	}
	r := f.ledger(t).Status.Reservations[recordName("node-00")]
	if f.azure.deletes != 1 || r.Outcome != "Unknown" || r.ReleasedAt != nil {
		t.Fatalf("lost acceptance was replayed: %+v", r)
	}
}

func TestPendingDeletesCannotSupplyHealthyCapacity(t *testing.T) {
	f := newFixture(t, 21, 2)
	f.azure.target = 20
	f.tick(t)
	f.tick(t)
	f.tick(t)
	f.tick(t)
	if f.azure.deletes != 2 {
		t.Fatalf("wanted two allowed deletions, got %d", f.azure.deletes)
	}
	node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-01", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	node.Status.Conditions[0].Status = corev1.ConditionTrue
	if _, err := f.kube.CoreV1().Nodes().UpdateStatus(context.Background(), node, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.azure.operation = MachineOperation{Outcome: "Succeeded"}
	f.tick(t)
	for _, r := range f.ledger(t).Status.Reservations {
		if r.ReleasedAt != nil {
			t.Fatal("another pending deletion supplied replacement capacity")
		}
	}
}

func TestPollingDelaySurvivesRestart(t *testing.T) {
	f := newFixture(t, 11, 1)
	f.tick(t)
	f.tick(t)
	b := f.ledger(t)
	key := recordName("node-00")
	r := b.Status.Reservations[key]
	deadline := metav1.NewTime(f.now.Add(time.Minute))
	r.PollAfter = &deadline
	b.Status.Reservations[key] = r
	if _, err := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent").UpdateStatus(context.Background(), b, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	clear(f.controller.nextPoll)
	f.tick(t)
	if f.azure.polls != 0 {
		t.Fatal("restart ignored Retry-After")
	}
	f.now = deadline.Time
	f.tick(t)
	if f.azure.polls != 1 {
		t.Fatal("pending operation was not polled after its deadline")
	}
}

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

func TestLatePodBlocksMachineDeletion(t *testing.T) {
	f := newFixture(t, 11, 1)
	added := false
	f.kube.PrependReactor("patch", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		if added {
			return false, nil, nil
		}
		added = true
		pod := placementPod("late", "node-00")
		if err := f.kube.Tracker().Add(pod); err != nil {
			t.Fatal(err)
		}
		return false, nil, nil
	})
	f.tick(t)
	f.tick(t)
	if f.azure.deletes != 0 {
		t.Fatal("late pod did not block deletion")
	}
}

func TestLostLedgerWithOwnershipCannotResetHistory(t *testing.T) {
	for _, swift := range []bool{false, true} {
		for _, emptyLedger := range []bool{false, true} {
			for _, mode := range []Mode{Audit, Enforce} {
				t.Run(fmt.Sprintf("swift=%v/empty=%v/%s", swift, emptyLedger, mode), func(t *testing.T) {
					var f *fixture
					if swift {
						f = swiftFixture(t)
					} else {
						f = newFixture(t, 11, 1)
					}
					f.tick(t)
					deletes := f.azure.deletes
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
					if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 || f.azure.deletes != deletes {
						t.Fatal("lost accounting allowed new writes")
					}
				})
			}
		}
	}
}

func TestPollFailureBackoffSurvivesRestart(t *testing.T) {
	f := newFixture(t, 11, 1)
	f.tick(t)
	f.tick(t)
	deadline := f.now.Add(2 * time.Minute)
	f.azure.operation.PollAfter = deadline
	f.azure.pollErr = errors.New("Azure throttled")
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("polling failure hidden")
	}
	r := f.ledger(t).Status.Reservations[recordName("node-00")]
	if r.PollAfter == nil || !r.PollAfter.Time.Equal(deadline) || r.Outcome != "Pending" || r.OperationToken == "" {
		t.Fatalf("backoff lost pending operation: %+v", r)
	}
	clear(f.controller.nextPoll)
	f.tick(t)
	if f.azure.polls != 1 {
		t.Fatal("restart ignored polling-error backoff")
	}
	f.azure.pollErr = nil
	f.now = deadline
	f.tick(t)
	if f.azure.polls != 2 || f.azure.deletes != 1 {
		t.Fatal("polling did not resume without replaying deletion")
	}
}

func TestPendingDeletionCannotSupplyWorkloadAvailability(t *testing.T) {
	f := swiftFixture(t)
	cfg, revision := f.controller.configuration()
	budget, err := f.controller.budget(context.Background(), revision, cfg.Mode)
	if err != nil {
		t.Fatal(err)
	}
	budget.Status.Reservations["pending"] = api.MitigationReservation{NodeUID: "node-01", InstanceID: "instance-01"}
	snapshot, err := f.controller.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	node := snapshot.Nodes[0]
	detections, _ := nodeEvidence(node, snapshot.Pods, snapshot.Events, f.now)
	if len(detections) != 1 {
		t.Fatalf("wanted one stalled pod: %+v", detections)
	}
	acted, err := f.controller.rescue(context.Background(), cfg, revision, node, detections[0], budget, snapshot)
	if err == nil || !strings.Contains(err.Error(), "below floor") || acted || evictionCount(f.kube.Actions()) != 0 {
		t.Fatalf("pending deletion counted toward availability: acted=%v err=%v", acted, err)
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
	f := newFixture(t, 11, 1)
	b := &api.NodeMitigationBudget{ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
		Status: api.NodeMitigationBudgetStatus{Reservations: map[string]api.MitigationReservation{"existing": {NodeUID: "existing"}}}}
	if err := f.records.Tracker().Add(b); err != nil {
		t.Fatal(err)
	}
	f.records.ClearActions()
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("unsupported history silently migrated")
	}
	if len(mutations(f.records.Actions())) != 0 || f.azure.deletes != 0 {
		t.Fatal("unsupported ledger changed")
	}
}
