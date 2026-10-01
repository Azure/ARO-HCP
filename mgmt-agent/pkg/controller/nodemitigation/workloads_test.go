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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func TestAvailableWorkloadReplicaSetReads(t *testing.T) {
	for _, count := range []int{0, 100, 500} {
		t.Run(fmt.Sprintf("unrelated=%d", count), func(t *testing.T) {
			f := swiftFixture(t)
			ctx := context.Background()
			candidate, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for i := range count {
				name := fmt.Sprintf("unrelated-%d", i)
				rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
					Name: name, Namespace: "test", UID: types.UID(name),
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "apps/v1", Kind: "Deployment", Name: name, UID: types.UID(name), Controller: ptr.To(true),
					}},
				}}
				pod := placementPod(name, "node-01")
				pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}
				pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
				for _, obj := range []runtime.Object{rs, pod} {
					if err := f.kube.Tracker().Add(obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			snapshot, err := f.controller.snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			for range 2 {
				if _, err := f.controller.availableWorkload(ctx, candidate, f.cfg, snapshot, nil); err != nil {
					t.Fatal(err)
				}
			}
			gets, lists, unrelatedGets := 0, 0, 0
			for _, action := range f.kube.Actions() {
				if action.GetResource().Resource != "replicasets" {
					continue
				}
				if action.GetNamespace() != "test" {
					t.Fatalf("ReplicaSet read outside workload namespace: %v", action)
				}
				switch action.GetVerb() {
				case "get":
					gets++
					if action.(ktesting.GetAction).GetName() != "router-rs" {
						unrelatedGets++
					}
				case "list":
					lists++
				default:
					t.Fatalf("unexpected ReplicaSet action: %v", action)
				}
			}
			if gets != 2 || lists != 2 || unrelatedGets != 0 {
				t.Fatalf("two admission checks made %d GETs (%d unrelated) and %d LISTs, want 2 GETs, 0 unrelated, 2 LISTs", gets, unrelatedGets, lists)
			}
		})
	}
}

func TestAvailableWorkloadReplicaSetIdentity(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*appsv1.ReplicaSet, *corev1.Pod)
		held      bool
		excluded  map[string]bool
		available *int32
	}{
		{name: "another ReplicaSet of the same Deployment"},
		{name: "different Deployment", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			rs.OwnerReferences[0].UID = "different-deployment"
		}},
		{name: "wrong parent kind", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			rs.OwnerReferences[0].Kind = "StatefulSet"
		}},
		{name: "wrong parent API", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			rs.OwnerReferences[0].APIVersion = "example.com/v1"
		}},
		{name: "noncontroller parent", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			rs.OwnerReferences[0].Controller = ptr.To(false)
		}},
		{name: "terminating ReplicaSet", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			now := metav1.Now()
			rs.DeletionTimestamp = &now
		}},
		{name: "reused ReplicaSet name", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			rs.UID = "replacement-replicaset"
		}},
		{name: "wrong ReplicaSet name", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			pod.OwnerReferences[0].Name = "missing-replicaset"
		}},
		{name: "ReplicaSet in another namespace", held: true, change: func(rs *appsv1.ReplicaSet, _ *corev1.Pod) {
			rs.Namespace = "other"
		}},
		{name: "wrong Pod owner kind", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			pod.OwnerReferences[0].Kind = "StatefulSet"
		}},
		{name: "wrong Pod owner API", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			pod.OwnerReferences[0].APIVersion = "example.com/v1"
		}},
		{name: "noncontroller Pod owner", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			pod.OwnerReferences[0].Controller = ptr.To(false)
		}},
		{name: "unready Pod", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			pod.Status.Conditions = nil
		}},
		{name: "terminating Pod", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			now := metav1.Now()
			pod.DeletionTimestamp = &now
		}},
		{name: "unknown node", held: true, change: func(_ *appsv1.ReplicaSet, pod *corev1.Pod) {
			pod.Spec.NodeName = "unknown"
		}},
		{name: "excluded node", held: true, excluded: map[string]bool{"node-01": true}},
		{name: "Deployment availability cap", held: true, available: ptr.To(int32(0))},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := swiftFixture(t)
			ctx := context.Background()
			rs, err := f.kube.AppsV1().ReplicaSets("test").Get(ctx, "router-rs", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			rs.Name, rs.UID = "available-rs", "available-rs"
			pod, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-healthy", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))}
			if test.change != nil {
				test.change(rs, pod)
			}
			if err := f.kube.Tracker().Add(rs); err != nil {
				t.Fatal(err)
			}
			if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, "test"); err != nil {
				t.Fatal(err)
			}
			if test.available != nil {
				deployment, err := f.kube.AppsV1().Deployments("test").Get(ctx, "router", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				deployment.Status.AvailableReplicas = *test.available
				if err := f.kube.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), deployment, "test"); err != nil {
					t.Fatal(err)
				}
			}
			candidate, err := f.kube.CoreV1().Pods("test").Get(ctx, "router-stalled", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := f.controller.snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.controller.availableWorkload(ctx, candidate, f.cfg, snapshot, test.excluded)
			if (err != nil) != test.held {
				t.Fatalf("held=%t, error=%v", test.held, err)
			}
			if test.held && !strings.Contains(err.Error(), "workload availability 0 is below floor 1") {
				t.Fatalf("unexpected hold reason: %v", err)
			}
		})
	}
}

func TestReplicaSetListFailureHoldsEviction(t *testing.T) {
	for _, afterClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("afterClaim=%t", afterClaim), func(t *testing.T) {
			f := swiftFixture(t)
			if err := f.records.Tracker().Add(&capacityreportv1alpha1.NodeMitigationBudget{
				ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
				Status:     capacityreportv1alpha1.NodeMitigationBudgetStatus{Version: 1},
			}); err != nil {
				t.Fatal(err)
			}
			claims := 0
			f.kube.PrependReactor("patch", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
				claims++
				return false, nil, nil
			})
			f.kube.PrependReactor("list", "replicasets", func(ktesting.Action) (bool, runtime.Object, error) {
				if !afterClaim || claims > 0 {
					return true, nil, errors.New("ReplicaSet list unavailable")
				}
				return false, nil, nil
			})
			f.records.ClearActions()
			err := f.controller.reconcile(context.Background())
			if err == nil || !strings.Contains(err.Error(), "ReplicaSet list unavailable") {
				t.Fatalf("failed ReplicaSet list did not hold: %v", err)
			}
			wantClaims := 0
			if afterClaim {
				wantClaims = 1
			}
			if claims != wantClaims || evictionCount(f.kube.Actions()) != 0 || len(mutations(f.records.Actions())) != 0 {
				t.Fatalf("failed list allowed accounting or eviction, claims=%d", claims)
			}
		})
	}
}

func TestSupportedPodRejectionGuards(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*corev1.Pod)
		want   string
	}{
		{"mirror pod", func(p *corev1.Pod) {
			p.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "static-hash"}
		}, "static, host-namespace or debug workload"},
		{"host network", func(p *corev1.Pod) { p.Spec.HostNetwork = true }, "static, host-namespace or debug workload"},
		{"host PID", func(p *corev1.Pod) { p.Spec.HostPID = true }, "static, host-namespace or debug workload"},
		{"host IPC", func(p *corev1.Pod) { p.Spec.HostIPC = true }, "static, host-namespace or debug workload"},
		{"ephemeral container", func(p *corev1.Pod) {
			p.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}}
		}, "static, host-namespace or debug workload"},
		{"custom scheduler", func(p *corev1.Pod) { p.Spec.SchedulerName = "custom" }, "unsupported scheduler"},
		{"scheduling gate", func(p *corev1.Pod) {
			p.Spec.SchedulingGates = []corev1.PodSchedulingGate{{Name: "example.com/gate"}}
		}, "unsupported scheduling gates or resource claims"},
		{"resource claim", func(p *corev1.Pod) {
			p.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "claim", ResourceClaimName: ptr.To("claim")}}
		}, "unsupported scheduling gates or resource claims"},
		{"persistent volume", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
			}}}
		}, "unsupported stateful or local volume"},
		{"host path", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: "/data"},
			}}}
		}, "unsupported stateful or local volume"},
		{"CSI volume", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
				CSI: &corev1.CSIVolumeSource{Driver: "example.com"},
			}}}
		}, "unsupported stateful or local volume"},
		{"ephemeral volume", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
				Ephemeral: &corev1.EphemeralVolumeSource{},
			}}}
		}, "unsupported stateful or local volume"},
		{"unknown volume", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{Name: "data"}}
		}, "unsupported stateful or local volume"},
		{"container host port", func(p *corev1.Pod) {
			p.Spec.Containers[0].Ports = []corev1.ContainerPort{{ContainerPort: 8080, HostPort: 8080}}
		}, "host ports require"},
		{"init container host port", func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "init", Ports: []corev1.ContainerPort{{ContainerPort: 8080, HostPort: 8080}}}}
		}, "host ports require"},
		{"finalizer", func(p *corev1.Pod) { p.Finalizers = []string{"example.com/protect"} }, "pod finalizers require"},
		{"missing grace period", func(p *corev1.Pod) { p.Spec.TerminationGracePeriodSeconds = nil }, "positive termination grace period"},
		{"zero grace period", func(p *corev1.Pod) { p.Spec.TerminationGracePeriodSeconds = ptr.To(int64(0)) }, "positive termination grace period"},
		{"negative grace period", func(p *corev1.Pod) { p.Spec.TerminationGracePeriodSeconds = ptr.To(int64(-1)) }, "positive termination grace period"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := swiftFixture(t)
			pod, err := f.kube.CoreV1().Pods("test").Get(context.Background(), "router-stalled", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			test.change(pod)
			if err := supportedPod(pod); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("guard error = %v, want %q", err, test.want)
			}
			if _, _, err := workload(context.Background(), f.kube, pod, f.cfg); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("workload error = %v, want %q", err, test.want)
			}
			if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, pod.Namespace); err != nil {
				t.Fatal(err)
			}
			if err := f.records.Tracker().Add(&capacityreportv1alpha1.NodeMitigationBudget{
				ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
				Status:     capacityreportv1alpha1.NodeMitigationBudgetStatus{Version: 1},
			}); err != nil {
				t.Fatal(err)
			}
			f.syncCaches(t)
			f.kube.ClearActions()
			f.records.ClearActions()
			err = f.controller.reconcile(context.Background())
			if pod.Spec.HostNetwork {
				// Host-network Pods are excluded by detection before admission.
				if err != nil {
					t.Fatalf("excluded host-network Pod reached admission: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("admission error = %v, want %q", err, test.want)
			}
			if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
				t.Fatal("unsupported workload caused ownership, accounting or eviction writes")
			}
		})
	}
}

func TestSupportedPodAcceptedForms(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*corev1.Pod)
	}{
		{"default pod", func(*corev1.Pod) {}},
		{"default scheduler", func(p *corev1.Pod) { p.Spec.SchedulerName = corev1.DefaultSchedulerName }},
		{"container ports", func(p *corev1.Pod) {
			p.Spec.Containers[0].Ports = []corev1.ContainerPort{{ContainerPort: 8080}}
			p.Spec.InitContainers = []corev1.Container{{Name: "init", Ports: []corev1.ContainerPort{{ContainerPort: 8080}}}}
		}},
		{"supported volumes", func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{
				{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}},
				{Name: "secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{}}},
				{Name: "projected", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{}}},
				{Name: "downward", VolumeSource: corev1.VolumeSource{DownwardAPI: &corev1.DownwardAPIVolumeSource{}}},
				{Name: "empty", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pod := placementPod("supported", "node")
			test.change(pod)
			if err := supportedPod(pod); err != nil {
				t.Fatalf("supported Pod rejected: %v", err)
			}
		})
	}
}
