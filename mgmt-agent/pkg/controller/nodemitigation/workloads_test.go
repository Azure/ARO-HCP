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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

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
			if err := f.records.Tracker().Add(&api.NodeMitigationBudget{
				ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: "mgmt-agent"},
				Status:     api.NodeMitigationBudgetStatus{Version: 1},
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
