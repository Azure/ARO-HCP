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
)

func swiftFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, 11, 0)
	selector := metav1.LabelSelector{MatchLabels: map[string]string{"app": "router"}}
	f.cfg.Workloads = []WorkloadPolicy{{
		NamespaceSelector: selector, PodSelector: selector, DeploymentSelector: selector,
		AllowUnhealthyDeletion: true,
	}}
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "router", Namespace: "test", UID: "deployment", Generation: 1, Labels: selector.MatchLabels,
	}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(2)), Selector: &selector},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 1},
	}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "router-rs", Namespace: "test", UID: "replicaset",
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
	}, Spec: appsv1.ReplicaSetSpec{Replicas: ptr.To(int32(2)), Selector: &selector}}
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
		Source:         corev1.EventSource{Host: pod.Spec.NodeName}, Reason: "FailedCreatePodSandBox",
		Message:        "route ip+net: no such network interface",
		FirstTimestamp: metav1.NewTime(f.now.Add(-time.Minute)), LastTimestamp: metav1.NewTime(f.now),
	}
	for _, object := range []runtime.Object{deployment, rs, pod, healthy, event,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: selector.MatchLabels}},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: "router", Namespace: "test", UID: "pdb", ResourceVersion: "1", Generation: 1},
			Spec:   policyv1.PodDisruptionBudgetSpec{Selector: &selector},
			Status: policyv1.PodDisruptionBudgetStatus{ObservedGeneration: 1}},
	} {
		if err := f.kube.Tracker().Add(object); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestSwiftRescueDrainAndDelete(t *testing.T) {
	f := swiftFixture(t)
	evictions := 0
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		evictions++
		eviction := action.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		if eviction.DeleteOptions == nil || eviction.DeleteOptions.Preconditions == nil ||
			eviction.DeleteOptions.Preconditions.UID == nil || *eviction.DeleteOptions.Preconditions.UID != "router-stalled" ||
			eviction.DeleteOptions.Preconditions.ResourceVersion == nil || eviction.DeleteOptions.GracePeriodSeconds != nil {
			t.Fatal("eviction must preserve UID, resourceVersion and graceful termination")
		}
		object, err := f.kube.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "test", "router-stalled")
		if err != nil {
			t.Fatal(err)
		}
		pod := object.(*corev1.Pod)
		if err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", pod.Name); err != nil {
			t.Fatal(err)
		}
		replacement := pod.DeepCopy()
		replacement.Name, replacement.UID, replacement.Spec.NodeName = "router-replacement", "replacement", "node-02"
		replacement.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
		if err := f.kube.Tracker().Add(replacement); err != nil {
			t.Fatal(err)
		}
		object, err = f.kube.Tracker().Get(appsv1.SchemeGroupVersion.WithResource("deployments"), "test", "router")
		if err != nil {
			t.Fatal(err)
		}
		deployment := object.(*appsv1.Deployment)
		deployment.Status.AvailableReplicas = 2
		if err := f.kube.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), deployment, "test"); err != nil {
			t.Fatal(err)
		}
		return true, nil, nil
	})
	for i := 0; i < 16; i++ {
		f.tick(t)
	}
	if evictions != 1 {
		t.Fatalf("expected one eviction, got %d", evictions)
	}
	episode := f.episode(t)
	if episode.Status.Phase != PhaseObserve || episode.Status.Recovery != nil || episode.Status.NodeDeletedAt == nil {
		t.Fatalf("cleanup did not reach observation: %+v", episode.Status)
	}
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "pods" {
			t.Fatal("rescue bypassed eviction")
		}
	}
	if f.ledger(t).Status.Reservations[episode.Name].ReleasedAt != nil {
		t.Fatal("instance still exists")
	}
}

func TestPDBDenialMustNotBeGenericThrottling(t *testing.T) {
	f := swiftFixture(t)
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		return true, nil, apierrors.NewTooManyRequests("API throttled", 1)
	})
	f.now = f.now.Add(2 * time.Second)
	if err := f.controller.reconcile(context.Background()); !apierrors.IsTooManyRequests(err) {
		t.Fatalf("expected throttling, got %v", err)
	}
	if intent := f.episode(t).Status.Intent; intent == nil || intent.Kind != "EvictPod" {
		t.Fatal("throttling authorized a bypass")
	}
}

func TestConfirmedPDBDenialAndRecoveryBeforeBypass(t *testing.T) {
	f := swiftFixture(t)
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		err := apierrors.NewTooManyRequests("disruption budget", 1)
		err.ErrStatus.Details.Causes = []metav1.StatusCause{{Type: policyv1.DisruptionBudgetCause}}
		return true, nil, err
	})
	f.tick(t)
	if intent := f.episode(t).Status.Intent; intent == nil || intent.Kind != "DeleteUnhealthyPod" {
		t.Fatal("confirmed denial did not prepare explicit fallback")
	}
	pod, err := f.kube.CoreV1().Pods("test").Get(context.Background(), "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue})
	if _, err := f.kube.CoreV1().Pods("test").UpdateStatus(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.kube.ClearActions()
	f.tick(t)
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "pods" {
			t.Fatal("recovered pod was deleted through bypass")
		}
	}
}

func TestGracefulBypassRequiresUnchangedUniqueBlockingPDB(t *testing.T) {
	for _, change := range []string{"none", "version", "overlap", "always-allow"} {
		t.Run(change, func(t *testing.T) {
			f := swiftFixture(t)
			ctx := context.Background()
			for i := 0; i < 6; i++ {
				f.tick(t)
			}
			f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if action.GetSubresource() != "eviction" {
					return false, nil, nil
				}
				err := apierrors.NewTooManyRequests("disruption budget", 1)
				err.ErrStatus.Details.Causes = []metav1.StatusCause{{Type: policyv1.DisruptionBudgetCause}}
				return true, nil, err
			})
			f.tick(t)
			if f.episode(t).Status.Intent.Kind != "DeleteUnhealthyPod" {
				t.Fatal("confirmed denial did not prepare the explicit fallback")
			}
			pdb, err := f.kube.PolicyV1().PodDisruptionBudgets("test").Get(ctx, "router", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "version":
				pdb.ResourceVersion = "2"
			case "always-allow":
				pdb.Spec.UnhealthyPodEvictionPolicy = ptr.To(policyv1.AlwaysAllow)
			case "overlap":
				other := pdb.DeepCopy()
				other.Name, other.UID = "other", "other"
				if _, err := f.kube.PolicyV1().PodDisruptionBudgets("test").Create(ctx, other, metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.kube.PolicyV1().PodDisruptionBudgets("test").Update(ctx, pdb, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			f.tick(t)
			deleted := false
			for _, action := range f.kube.Actions() {
				if action.GetVerb() != "delete" || action.GetResource().Resource != "pods" {
					continue
				}
				deleted = true
				options := action.(ktesting.DeleteAction).GetDeleteOptions()
				if options.GracePeriodSeconds != nil || options.Preconditions == nil ||
					options.Preconditions.UID == nil || *options.Preconditions.UID != "router-stalled" ||
					options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != "1" {
					t.Fatal("bypass lost graceful termination or identity preconditions")
				}
			}
			if deleted != (change == "none") {
				t.Fatalf("PDB change %q: unexpected direct deletion %v", change, deleted)
			}
		})
	}
}

func TestSerialAndLateRescueWithDrainDisabled(t *testing.T) {
	f := swiftFixture(t)
	ctx := context.Background()
	f.cfg.Drain, f.cfg.DeleteNode = false, false
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	original, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	addStalled := func(name string, replicas int32) {
		t.Helper()
		pod := original.DeepCopy()
		pod.Name, pod.UID = name, types.UID(name)
		if err := f.kube.Tracker().Add(pod); err != nil {
			t.Fatal(err)
		}
		event, err := f.kube.CoreV1().Events("test").Get(ctx, "sandbox", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		event.Name = name
		event.InvolvedObject.Name, event.InvolvedObject.UID = name, pod.UID
		event.FirstTimestamp, event.LastTimestamp = metav1.NewTime(f.now.Add(-time.Minute)), metav1.NewTime(f.now)
		if err := f.kube.Tracker().Add(event); err != nil {
			t.Fatal(err)
		}
		deployment, err := f.kube.AppsV1().Deployments("test").Get(ctx, "router", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		deployment.Spec.Replicas = ptr.To(replicas)
		deployment.Status.Replicas, deployment.Status.UpdatedReplicas = replicas, replicas
		if err := f.kube.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), deployment, "test"); err != nil {
			t.Fatal(err)
		}
	}
	addStalled("second-stalled", 3)
	evicted := map[string]bool{}
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		eviction := action.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		object, err := f.kube.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "test", eviction.Name)
		if err != nil {
			t.Fatal(err)
		}
		pod := object.(*corev1.Pod).DeepCopy()
		evicted[pod.Name] = true
		if err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", pod.Name); err != nil {
			t.Fatal(err)
		}
		pod.Name, pod.UID, pod.Spec.NodeName = "replacement-"+pod.Name, types.UID("replacement-"+string(pod.UID)), "node-02"
		pod.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
		if err := f.kube.Tracker().Add(pod); err != nil {
			t.Fatal(err)
		}
		object, err = f.kube.Tracker().Get(appsv1.SchemeGroupVersion.WithResource("deployments"), "test", "router")
		if err != nil {
			t.Fatal(err)
		}
		deployment := object.(*appsv1.Deployment)
		deployment.Status.AvailableReplicas++
		if err := f.kube.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), deployment, "test"); err != nil {
			t.Fatal(err)
		}
		return true, nil, nil
	})
	for i := 0; i < 20; i++ {
		f.tick(t)
	}
	if len(evicted) != 2 || f.episode(t).Status.Phase != PhaseDrain {
		t.Fatalf("serial rescue did not reach the drain hold: %v, %+v", evicted, f.episode(t).Status)
	}
	addStalled("late-stalled", 4)
	for i := 0; i < 10; i++ {
		f.tick(t)
	}
	if !evicted["late-stalled"] || f.episode(t).Status.NodeDeletedAt != nil {
		t.Fatalf("late rescue failed or bypassed the drain hold: %v", evicted)
	}
}

func TestBoundOwnerTemplateCannotPromiseReplacementPlacement(t *testing.T) {
	for _, owner := range []string{"replicaset", "deployment"} {
		t.Run(owner, func(t *testing.T) {
			f := swiftFixture(t)
			ctx := context.Background()
			pod, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if owner == "replicaset" {
				rs, err := f.kube.AppsV1().ReplicaSets("test").Get(ctx, "router-rs", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				rs.Spec.Template.Spec.NodeName = pod.Spec.NodeName
				if _, err := f.kube.AppsV1().ReplicaSets("test").Update(ctx, rs, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				deployment, err := f.kube.AppsV1().Deployments("test").Get(ctx, "router", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				deployment.Spec.Template.Spec.NodeName = pod.Spec.NodeName
				if _, err := f.kube.AppsV1().Deployments("test").Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := workload(ctx, f.kube, pod, f.cfg); err == nil {
				t.Fatal("a node-bound owner template was accepted")
			}
		})
	}
}

func TestReregistrationRescuesGapPodWithoutAnotherNodeDelete(t *testing.T) {
	f := swiftFixture(t)
	ctx := context.Background()
	original, err := f.kube.CoreV1().Nodes().Get(ctx, "node-00", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original.Status.Conditions[0].Status = corev1.ConditionFalse
	if _, err := f.kube.CoreV1().Nodes().UpdateStatus(ctx, original, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", pod.Name); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	if f.episode(t).Status.Phase != PhaseObserve {
		t.Fatal("initial Node deletion not observed")
	}
	f.cfg.Drain, f.cfg.DeleteNode = false, false
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	original.UID, original.ResourceVersion = "reregistered", "2"
	original.Status.Conditions[0].Status = corev1.ConditionTrue
	if _, err := f.kube.CoreV1().Nodes().Create(ctx, original, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod.Name, pod.UID = "router-gap", "gap"
	if err := f.kube.Tracker().Add(pod); err != nil {
		t.Fatal(err)
	}
	event, err := f.kube.CoreV1().Events("test").Get(ctx, "sandbox", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	event.InvolvedObject.Name, event.InvolvedObject.UID = pod.Name, pod.UID
	event.FirstTimestamp, event.LastTimestamp = metav1.NewTime(f.now.Add(-time.Minute)), metav1.NewTime(f.now)
	if _, err := f.kube.CoreV1().Events("test").Update(ctx, event, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	evictions := 0
	f.kube.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		evictions++
		eviction := action.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		if eviction.Name != pod.Name || *eviction.DeleteOptions.Preconditions.UID != pod.UID {
			t.Fatal("wrong rescue identity")
		}
		return true, nil, f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", pod.Name)
	})
	f.kube.ClearActions()
	for i := 0; i < 7; i++ {
		f.tick(t)
	}
	if evictions != 1 {
		t.Fatalf("expected a gap-pod rescue, got %d", evictions)
	}
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "nodes" {
			t.Fatal("re-registration replayed Node DELETE")
		}
	}
	current, err := f.kube.CoreV1().Nodes().Get(ctx, original.Name, metav1.GetOptions{})
	if err != nil || !current.Spec.Unschedulable || current.UID != original.UID {
		t.Fatalf("quarantine missing: %v", err)
	}
	if len(f.ledger(t).Status.Reservations) != 1 {
		t.Fatal("re-registration consumed a second reservation")
	}
}

func TestStaleDeploymentAvailabilityDoesNotDeadlockRescue(t *testing.T) {
	f := swiftFixture(t)
	ctx := context.Background()
	other, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-healthy", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other.Status.Conditions[0].Status = corev1.ConditionFalse
	if _, err := f.kube.CoreV1().Pods("test").UpdateStatus(ctx, other, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	recovery := f.episode(t).Status.Recovery
	if recovery == nil || recovery.Replicas != 1 {
		t.Fatalf("stale Deployment status inflated recovery target: %+v", recovery)
	}
	pod, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "test", pod.Name); err != nil {
		t.Fatal(err)
	}
	pod.Name, pod.UID, pod.Spec.NodeName = "replacement", "replacement", "node-02"
	pod.Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
	if err := f.kube.Tracker().Add(pod); err != nil {
		t.Fatal(err)
	}
	recovered, err := f.controller.recovered(ctx, recovery, "instance-00")
	if err != nil || !recovered {
		t.Fatalf("one actual replacement must permit serial rescue: %v", err)
	}
}
