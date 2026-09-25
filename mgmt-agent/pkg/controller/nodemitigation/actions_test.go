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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

func swiftFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, 11)
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
	if evictionCount(f.kube.Actions()) != 1 {
		t.Fatal("SWIFT did not submit exactly one eviction")
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

func TestEvictionRechecksRegisteredDetectorScope(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*corev1.Node)
		want   string
	}{
		{"SWIFT scope removed", func(n *corev1.Node) { delete(n.Labels, detectors.SwiftV2LabelKey) }, "pod fault evidence expired or recovered"},
		{"node deleting", func(n *corev1.Node) { n.DeletionTimestamp = &metav1.Time{Time: time.Unix(1, 0)} }, "source Node identity or readiness changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := swiftFixture(t)
			ctx := context.Background()
			node, err := f.kube.CoreV1().Nodes().Get(ctx, "node-00", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			pod, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := f.controller.snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			live := node.DeepCopy()
			test.change(live)
			if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("nodes"), live, ""); err != nil {
				t.Fatal(err)
			}
			_, _, err = f.controller.evictionTarget(ctx, f.cfg, node, pod, snapshot, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("registered detector scope did not block admission: %v", err)
			}
			if evictionCount(f.kube.Actions()) != 0 {
				t.Fatal("evicted without a live registered detection")
			}
		})
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

func TestSwiftDisabledMakesNoWrites(t *testing.T) {
	f := swiftFixture(t)
	if err := f.controller.SetConfig(Default()); err != nil {
		t.Fatal(err)
	}
	f.kube.ClearActions()
	f.records.ClearActions()
	f.tick(t)
	if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
		t.Fatal("disabled mitigation wrote state")
	}
}

func TestSwiftConfigurationChangeFencesAdmission(t *testing.T) {
	for _, mode := range []Mode{Disabled, Audit, Enforce} {
		t.Run(string(mode), func(t *testing.T) {
			f := swiftFixture(t)
			reads := 0
			f.kube.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if action.(ktesting.GetAction).GetName() == "router-stalled" {
					reads++
					if reads == 2 {
						f.cfg.Mode = mode
						if err := f.controller.SetConfig(f.cfg); err != nil {
							t.Fatal(err)
						}
					}
				}
				return false, nil, nil
			})
			if err := f.controller.reconcile(context.Background()); !errors.Is(err, ErrPaused) {
				t.Fatalf("configuration change was not fenced: %v", err)
			}
			if reads != 2 || evictionCount(f.kube.Actions()) != 0 || len(f.ledger(t).Status.Evictions) != 0 {
				t.Fatal("configuration change admitted a stale eviction attempt")
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

func TestPodOwnershipRecheckedBeforeEviction(t *testing.T) {
	for _, alreadyOwned := range []bool{false, true} {
		for _, owner := range []string{"", "another-controller", ControllerName} {
			t.Run(fmt.Sprintf("owned=%t/owner=%q", alreadyOwned, owner), func(t *testing.T) {
				f := swiftFixture(t)
				resource := corev1.SchemeGroupVersion.WithResource("pods")
				if alreadyOwned {
					obj, err := f.kube.Tracker().Get(resource, "test", "router-stalled")
					if err != nil {
						t.Fatal(err)
					}
					pod := obj.(*corev1.Pod)
					pod.Labels[ownershipLabel] = ControllerName
					if err := f.kube.Tracker().Update(resource, pod, "test"); err != nil {
						t.Fatal(err)
					}
					if err := f.records.Tracker().Add(&api.NodeMitigationBudget{
						ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
						Status:     api.NodeMitigationBudgetStatus{Version: 1},
					}); err != nil {
						t.Fatal(err)
					}
				}
				reads := 0
				f.kube.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
					if action.(ktesting.GetAction).GetName() != "router-stalled" {
						return false, nil, nil
					}
					reads++
					if reads != 2 {
						return false, nil, nil
					}
					obj, err := f.kube.Tracker().Get(resource, "test", "router-stalled")
					if err != nil {
						t.Fatal(err)
					}
					pod := obj.(*corev1.Pod)
					if pod.Labels[ownershipLabel] != ControllerName {
						t.Fatal("post-claim race did not start from controller ownership")
					}
					if owner == "" {
						delete(pod.Labels, ownershipLabel)
					} else {
						pod.Labels[ownershipLabel] = owner
					}
					pod.ResourceVersion = "2"
					if err := f.kube.Tracker().Update(resource, pod, "test"); err != nil {
						t.Fatal(err)
					}
					return false, nil, nil
				})
				err := f.controller.reconcile(context.Background())
				if reads != 2 {
					t.Fatalf("post-claim read not exercised: %d", reads)
				}
				want := 1
				if owner != ControllerName {
					want = 0
					if err == nil || !strings.Contains(err.Error(), "ownership changed") {
						t.Fatalf("ownership loss not reported: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if evictionCount(f.kube.Actions()) != want || len(f.ledger(t).Status.Evictions) != want {
					t.Fatal("post-claim ownership did not guard accounting and eviction")
				}
			})
		}
	}
}

func TestPlacementRecheckedAfterClaim(t *testing.T) {
	for _, test := range []struct {
		name        string
		node        func(*corev1.Node)
		podName     string
		pod         func(*corev1.Pod)
		change      func(*testing.T, *fixture)
		onFinalRead bool
		wantError   string
	}{
		{name: "capacity unchanged"},
		{name: "new scheduled pod", wantError: "no feasible replacement capacity", change: func(t *testing.T, f *fixture) {
			pod := placementPod("new-resident", "node-01")
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("7")
			if err := f.kube.Tracker().Add(pod); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "new NIC allocation", wantError: "no feasible replacement capacity", change: func(t *testing.T, f *fixture) {
			var interfaces []any
			for i := 0; i < 6; i++ {
				interfaces = append(interfaces, map[string]any{"ncID": fmt.Sprintf("nc-%d", i)})
			}
			nic := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "multitenancy.acn.azure.com/v1alpha1", "kind": "MultitenantPodNetworkConfig",
				"metadata": map[string]any{"name": "new-allocation", "namespace": "test"},
				"spec":     map[string]any{"podUID": "deleted-pod", "podNetwork": "network"},
				"status":   map[string]any{"nodeName": "node-01", "interfaceInfos": interfaces},
			}}
			if err := f.dynamic.Tracker().Add(nic); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "destination tainted", wantError: "no feasible replacement capacity", node: func(node *corev1.Node) {
			node.Spec.Taints = []corev1.Taint{{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}}
		}},
		{name: "destination capacity reduced", wantError: "no feasible replacement capacity", node: func(node *corev1.Node) {
			node.Status.Allocatable[corev1.ResourceCPU] = resource.MustParse("1")
		}},
		{name: "destination label removed", wantError: "no feasible replacement capacity", node: func(node *corev1.Node) {
			delete(node.Labels, "placement-target")
		}},
		{name: "resident resized", podName: "router-healthy", wantError: "no feasible replacement capacity", pod: func(pod *corev1.Pod) {
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")
		}},
		{name: "target resized", podName: "router-stalled", wantError: "no feasible replacement capacity", pod: func(pod *corev1.Pod) {
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")
		}},
		{name: "target resized after snapshot", podName: "router-stalled", onFinalRead: true, wantError: "no feasible replacement capacity", pod: func(pod *corev1.Pod) {
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")
		}},
		{name: "node snapshot read fails", wantError: "nodes unavailable", change: func(_ *testing.T, f *fixture) {
			f.kube.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("nodes unavailable")
			})
		}},
		{name: "NIC snapshot read fails", wantError: "allocations unavailable", change: func(_ *testing.T, f *fixture) {
			f.dynamic.PrependReactor("list", "multitenantpodnetworkconfigs", func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("allocations unavailable")
			})
		}},
		{name: "snapshot expires while reading NICs", wantError: "cluster snapshot is stale", change: func(_ *testing.T, f *fixture) {
			f.dynamic.PrependReactor("list", "multitenantpodnetworkconfigs", func(ktesting.Action) (bool, runtime.Object, error) {
				f.now = f.now.Add(f.cfg.ObservationMaxAge.Duration + time.Second)
				return false, nil, nil
			})
		}},
		{name: "snapshot expires during final admission", wantError: "cluster snapshot is stale", change: func(_ *testing.T, f *fixture) {
			f.kube.PrependReactor("get", "deployments", func(ktesting.Action) (bool, runtime.Object, error) {
				f.now = f.now.Add(f.cfg.ObservationMaxAge.Duration + time.Second)
				return false, nil, nil
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := swiftFixture(t)
			nodeResource := corev1.SchemeGroupVersion.WithResource("nodes")
			podResource := corev1.SchemeGroupVersion.WithResource("pods")
			obj, err := f.kube.Tracker().Get(nodeResource, "", "node-01")
			if err != nil {
				t.Fatal(err)
			}
			node := obj.(*corev1.Node)
			node.Labels["placement-target"] = "true"
			if err := f.kube.Tracker().Update(nodeResource, node, ""); err != nil {
				t.Fatal(err)
			}
			obj, err = f.kube.Tracker().Get(podResource, "test", "router-stalled")
			if err != nil {
				t.Fatal(err)
			}
			pod := obj.(*corev1.Pod)
			pod.Spec.NodeSelector = map[string]string{"placement-target": "true"}
			if err := f.kube.Tracker().Update(podResource, pod, "test"); err != nil {
				t.Fatal(err)
			}
			if err := f.records.Tracker().Add(&api.NodeMitigationBudget{
				ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
				Status:     api.NodeMitigationBudgetStatus{Version: 1},
			}); err != nil {
				t.Fatal(err)
			}
			f.syncCaches(t)
			changed := false
			change := func() {
				changed = true
				if test.node != nil {
					obj, err := f.kube.Tracker().Get(nodeResource, "", "node-01")
					if err != nil {
						t.Fatal(err)
					}
					node := obj.(*corev1.Node)
					test.node(node)
					if err := f.kube.Tracker().Update(nodeResource, node, ""); err != nil {
						t.Fatal(err)
					}
				}
				if test.pod != nil {
					obj, err := f.kube.Tracker().Get(podResource, "test", test.podName)
					if err != nil {
						t.Fatal(err)
					}
					pod := obj.(*corev1.Pod)
					test.pod(pod)
					if err := f.kube.Tracker().Update(podResource, pod, "test"); err != nil {
						t.Fatal(err)
					}
				}
				if test.change != nil {
					test.change(t, f)
				}
			}
			claims := 0
			f.kube.PrependReactor("patch", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				if action.(ktesting.PatchAction).GetName() == "router-stalled" {
					claims++
					if !test.onFinalRead {
						change()
					}
				}
				return false, nil, nil
			})
			if test.onFinalRead {
				reads := 0
				f.kube.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
					if action.(ktesting.GetAction).GetName() == "router-stalled" {
						reads++
						if reads == 2 {
							change()
						}
					}
					return false, nil, nil
				})
			}
			f.records.ClearActions()
			err = f.controller.reconcile(context.Background())
			if claims != 1 || !changed {
				t.Fatalf("claim race was not exercised: claims=%d changed=%v, err=%v", claims, changed, err)
			}
			want := 1
			if test.wantError != "" {
				want = 0
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("placement change not reported: %v, want %q", err, test.wantError)
				}
				if len(mutations(f.records.Actions())) != 0 {
					t.Fatal("failed final admission wrote accounting")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if evictionCount(f.kube.Actions()) != want || len(f.ledger(t).Status.Evictions) != want {
				t.Fatal("final placement did not guard accounting and eviction")
			}
		})
	}
}

func TestPlacementUsesAdmittedPod(t *testing.T) {
	for _, test := range []struct {
		name       string
		request    string
		missingPod bool
		wantError  string
	}{
		{name: "same node has capacity", request: "1"},
		{name: "resize charges resident and replacement", request: "5", wantError: "no feasible replacement capacity"},
		{name: "missing snapshot Pod", request: "1", missingPod: true, wantError: "missing from the cluster snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, 1)
			pod := placementPod("moving", "node-00")
			snapshot, err := f.controller.snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !test.missingPod {
				snapshot.Pods = []*corev1.Pod{pod.DeepCopy()}
			}
			snapshot.Faulted[pod.Spec.NodeName] = true
			snapshot.Detections[pod.Spec.NodeName] = []detectors.Detection{{
				Detector: detectors.SwiftPodSandboxStalled, Scope: detectors.PodScope,
				NodeUID: snapshot.Nodes[0].UID, PodUIDs: []types.UID{pod.UID},
			}}
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse(test.request)
			err = f.controller.checkPlacement(f.cfg, snapshot, pod, nil)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("admitted Pod placement error = %v, want %q", err, test.wantError)
			}
			if !snapshot.Faulted[pod.Spec.NodeName] {
				t.Fatal("placement mutated the shared fault verdict")
			}
			if !test.missingPod {
				request := snapshot.Pods[0].Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
				if request.Cmp(resource.MustParse("1")) != 0 {
					t.Fatal("placement mutated the shared Pod snapshot")
				}
			}
		})
	}
}

func TestPlacementPreservesIndependentFaults(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ClusterSnapshot, *corev1.Pod)
		held   bool
	}{
		{name: "candidate fault only"},
		{name: "node fault", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Detections[p.Spec.NodeName] = append(s.Detections[p.Spec.NodeName], detectors.Detection{
				Detector: "swift-vf-teardown", Scope: detectors.NodeScope, NodeUID: s.Nodes[0].UID,
			})
		}},
		{name: "another pod fault", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			other := s.Detections[p.Spec.NodeName][0]
			other.PodUIDs = []types.UID{"another-pod"}
			s.Detections[p.Spec.NodeName] = append(s.Detections[p.Spec.NodeName], other)
		}},
		{name: "multiple pod identities", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Detections[p.Spec.NodeName][0].PodUIDs = append(s.Detections[p.Spec.NodeName][0].PodUIDs, "another-pod")
		}},
		{name: "other detector", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Detections[p.Spec.NodeName][0].Detector = "another-detector"
		}},
		{name: "missing evidence", held: true, change: func(s *ClusterSnapshot, _ *corev1.Pod) {
			s.Detections = nil
		}},
		{name: "wrong node identity", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Detections[p.Spec.NodeName][0].NodeUID = "another-node"
		}},
		{name: "missing pod identity", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Detections[p.Spec.NodeName][0].PodUIDs = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			pod.Spec.NodeSelector = map[string]string{corev1.LabelHostname: pod.Spec.NodeName}
			if len(snapshot.Detections[pod.Spec.NodeName]) != 1 || !snapshot.Faulted[pod.Spec.NodeName] {
				t.Fatal("snapshot did not retain the candidate's scoped fault")
			}
			if test.change != nil {
				test.change(&snapshot, pod)
			}
			before, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			err = f.controller.checkPlacement(f.cfg, snapshot, pod, nil)
			if test.held {
				if err == nil || !strings.Contains(err.Error(), "no feasible replacement capacity") {
					t.Fatalf("independent fault did not hold placement: %v", err)
				}
			} else if err != nil {
				t.Fatalf("candidate-only fault blocked same-node placement: %v", err)
			}
			after, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("placement mutated shared snapshot evidence")
			}
		})
	}
}

func TestIndependentFaultGuardsAccountingAndEviction(t *testing.T) {
	for _, afterClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("afterClaim=%t", afterClaim), func(t *testing.T) {
			f := swiftFixture(t)
			nodeResource := corev1.SchemeGroupVersion.WithResource("nodes")
			podResource := corev1.SchemeGroupVersion.WithResource("pods")
			node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			node.Labels[corev1.LabelHostname] = node.Name
			node.Status.Allocatable[kuberesources.SwiftNICResourceName] = resource.MustParse("10")
			if err := f.kube.Tracker().Update(nodeResource, node, ""); err != nil {
				t.Fatal(err)
			}
			pod, err := f.kube.CoreV1().Pods("test").Get(context.Background(), "router-stalled", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			pod.Spec.NodeSelector = map[string]string{corev1.LabelHostname: node.Name}
			if err := f.kube.Tracker().Update(podResource, pod, pod.Namespace); err != nil {
				t.Fatal(err)
			}
			event, err := f.kube.CoreV1().Events("test").Get(context.Background(), "sandbox", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			addFault := func() {
				other := pod.DeepCopy()
				other.Name, other.UID = "another-stalled", "another-stalled"
				event := event.DeepCopy()
				event.Name = "another-sandbox"
				event.InvolvedObject.Name, event.InvolvedObject.UID = other.Name, other.UID
				for _, obj := range []runtime.Object{other, event} {
					if err := f.kube.Tracker().Add(obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			claims := 0
			f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
				claims++
				if afterClaim {
					addFault()
				}
				return false, nil, nil
			})
			if !afterClaim {
				addFault()
			}
			if err := f.records.Tracker().Add(&api.NodeMitigationBudget{
				ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
				Status:     api.NodeMitigationBudgetStatus{Version: 1},
			}); err != nil {
				t.Fatal(err)
			}
			f.syncCaches(t)
			f.records.ClearActions()
			err = f.controller.reconcile(context.Background())
			if err == nil || !strings.Contains(err.Error(), "no feasible replacement capacity") {
				t.Fatalf("independent fault did not block eviction: %v", err)
			}
			wantClaims := 0
			if afterClaim {
				wantClaims = 1
			}
			if claims != wantClaims {
				t.Fatalf("claims = %d, want %d", claims, wantClaims)
			}
			if evictionCount(f.kube.Actions()) != 0 || len(mutations(f.records.Actions())) != 0 ||
				len(f.ledger(t).Status.Evictions) != 0 {
				t.Fatal("independent fault permitted accounting or eviction")
			}
		})
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
