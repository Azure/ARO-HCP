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
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func swiftFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, 11, 0)
	selector := metav1.LabelSelector{MatchLabels: map[string]string{"app": "router"}}
	f.cfg.Workloads = []WorkloadPolicy{{NamespaceSelector: selector, PodSelector: selector, DeploymentSelector: selector, MinAvailableReplicas: ptr.To(int32(1))}}
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "test", UID: "deployment", Generation: 1, Labels: selector.MatchLabels},
		Spec:   appsv1.DeploymentSpec{Replicas: ptr.To(int32(2)), Selector: &selector},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 1}}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "router-rs", Namespace: "test", UID: "replicaset", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))}},
		Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(2)), Selector: &selector}}
	pod := placementPod("router-stalled", "node-00")
	pod.CreationTimestamp = metav1.NewTime(f.now.Add(-5 * time.Minute))
	pod.ResourceVersion = "1"
	pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}
	pod.Status = corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(f.now.Add(-2 * time.Minute))},
		{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(f.now.Add(-2 * time.Minute))},
	}}
	healthy := placementPod("router-healthy", "node-01")
	healthy.OwnerReferences = pod.OwnerReferences
	healthy.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
	event := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "sandbox", Namespace: "test"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID},
		Source:         corev1.EventSource{Host: pod.Spec.NodeName}, Reason: "FailedCreatePodSandBox", Message: "route ip+net: no such network interface",
		FirstTimestamp: metav1.NewTime(f.now.Add(-time.Minute)), LastTimestamp: metav1.NewTime(f.now)}
	for _, obj := range []runtime.Object{deployment, rs, pod, healthy, event, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: selector.MatchLabels}}} {
		if err := f.kube.Tracker().Add(obj); err != nil {
			t.Fatal(err)
		}
	}
	f.syncCaches(t)
	return f
}

func evictionCount(actions []ktesting.Action) int {
	count := 0
	for _, action := range actions {
		if action.GetSubresource() == "eviction" {
			count++
		}
	}
	return count
}

func TestSwiftEvictsWithoutCordonOrAzureDependency(t *testing.T) {
	f := swiftFixture(t)
	f.azure.poolErr = errors.New("Azure unavailable")
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		e := action.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		if e.DeleteOptions == nil || e.DeleteOptions.Preconditions == nil || *e.DeleteOptions.Preconditions.UID != "router-stalled" ||
			e.DeleteOptions.Preconditions.ResourceVersion == nil || e.DeleteOptions.GracePeriodSeconds != nil {
			t.Fatal("missing graceful identity guards")
		}
		return true, nil, nil
	})
	f.tick(t)
	if evictionCount(f.kube.Actions()) != 1 || f.azure.deletes != 0 || len(f.ledger(t).Status.Reservations) != 0 {
		t.Fatal("SWIFT used node deletion accounting")
	}
	for _, a := range f.kube.Actions() {
		if a.GetVerb() == "delete" || (a.GetVerb() == "patch" && a.GetResource().Resource == "nodes") {
			t.Fatalf("unexpected action: %v", a)
		}
	}
	if len(f.ledger(t).Status.Evictions) != 1 {
		t.Fatal("eviction not accounted")
	}
}

func TestEvictionDenialsNeverFallBackToDelete(t *testing.T) {
	for _, pdb := range []bool{false, true} {
		t.Run(fmt.Sprint(pdb), func(t *testing.T) {
			f := swiftFixture(t)
			err := apierrors.NewTooManyRequests("held", 1)
			if pdb {
				err.ErrStatus.Details = &metav1.StatusDetails{Causes: []metav1.StatusCause{{Type: policyv1.DisruptionBudgetCause}}}
			}
			f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if action.GetSubresource() == "eviction" {
					return true, nil, err
				}
				return false, nil, nil
			})
			if e := f.controller.reconcile(context.Background()); e == nil {
				t.Fatal("eviction denial hidden")
			}
			want := "throttled"
			if pdb {
				want = "pdb_denied"
			}
			if actionOutcome(err) != want {
				t.Fatal("denial classification")
			}
			for _, a := range f.kube.Actions() {
				if a.GetVerb() == "delete" {
					t.Fatal("denial bypassed by DELETE")
				}
			}
			if len(f.ledger(t).Status.Evictions) != 1 {
				t.Fatal("denied attempt reset rate allowance")
			}
		})
	}
}

func TestSwiftAuditAndAvailabilityFloors(t *testing.T) {
	for _, floor := range []int32{0, 1, 2} {
		t.Run(fmt.Sprint(floor), func(t *testing.T) {
			f := swiftFixture(t)
			f.cfg.Mode = Audit
			f.cfg.Workloads[0].MinAvailableReplicas = &floor
			if err := f.controller.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			err := f.controller.reconcile(context.Background())
			if (err != nil) != (floor > 1) {
				t.Fatalf("floor=%d err=%v", floor, err)
			}
			if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
				t.Fatal("audit wrote state")
			}
		})
	}
}

func TestEvictionRatesSurvivePodRecreationAndRestart(t *testing.T) {
	cfg := testConfig()
	now := time.Now()
	budget := api.NodeMitigationBudgetStatus{EvictionWindow: cfg.EvictionWindow, Evictions: map[string]api.EvictionRecord{}}
	for i := 0; i < cfg.MaxEvictionsPerWorkload; i++ {
		budget.Evictions[fmt.Sprint(i)] = api.EvictionRecord{WorkloadUID: "deployment", NodeUID: types.UID(fmt.Sprint(i)), PodUID: types.UID(fmt.Sprint(i)), AttemptedAt: metav1.NewTime(now.Add(-10 * time.Second))}
	}
	if err := evictionAllowance(budget, cfg, "deployment", "another-node", now); err == nil {
		t.Fatal("new pod/node identity reset workload limit")
	}
	if err := evictionAllowance(budget, cfg, "different-deployment", "different-node", now); err != nil {
		t.Fatal(err)
	}
	if err := evictionAllowance(budget, cfg, "deployment", "another-node", now.Add(cfg.EvictionWindow.Duration)); err != nil {
		t.Fatal(err)
	}
}

func TestPodRecoveryAfterOwnershipPreventsEviction(t *testing.T) {
	f := swiftFixture(t)
	f.kube.PrependReactor("patch", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj, err := f.kube.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "test", "router-stalled")
		if err != nil {
			t.Fatal(err)
		}
		p := obj.(*corev1.Pod)
		p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue})
		if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), p, "test"); err != nil {
			t.Fatal(err)
		}
		return true, p, nil
	})
	if err := f.controller.reconcile(context.Background()); err == nil {
		t.Fatal("recovery was not reported")
	}
	if evictionCount(f.kube.Actions()) != 0 {
		t.Fatal("recovered pod evicted")
	}
}

func TestSameNodeIsAValidPlacement(t *testing.T) {
	f := swiftFixture(t)
	snapshot, err := f.controller.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pod, err := f.kube.CoreV1().Pods("test").Get(context.Background(), "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range snapshot.Nodes {
		node.Labels[corev1.LabelHostname] = node.Name
	}
	pod.Spec.NodeSelector = map[string]string{corev1.LabelHostname: "node-00"}
	snapshot.Faulted["node-00"] = false
	if err := placement(snapshot, map[string]bool{}, []*corev1.Pod{pod}); err != nil {
		t.Fatalf("same-node placement rejected: %v", err)
	}
}
