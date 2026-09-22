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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func disposableFixture(t *testing.T) (*fixture, *corev1.Pod, *appsv1.DaemonSet) {
	t.Helper()
	f := newFixture(t, 11, 1)
	pod := placementPod("network", "node-00")
	pod.Spec.Containers = []corev1.Container{{Name: "agent", Image: "agent:v1"}}
	pod.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "network", UID: "ds", Controller: ptr.To(true)}}
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "network", Namespace: pod.Namespace, UID: "ds"}}
	ds.Spec.Template.Spec = *pod.Spec.DeepCopy()
	ds.Spec.Template.Spec.NodeName = ""
	templateHash, err := policyHash(ds.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	podHash, err := DisposablePodSpecHash(pod)
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.DisposableDaemonSets = []DaemonSetPolicy{{Namespace: pod.Namespace, Name: ds.Name, UID: ds.UID,
		Role: "networking", TemplateSHA256: templateHash, PodSpecSHA256: podHash}}
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	if err := f.kube.Tracker().Add(ds); err != nil {
		t.Fatal(err)
	}
	return f, pod, ds
}

func TestApprovedDisposableDaemonSetMayRemain(t *testing.T) {
	f, pod, _ := disposableFixture(t)
	if err := f.kube.Tracker().Add(pod); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	f.tick(t)
	if f.azure.deletes != 1 || evictionCount(f.kube.Actions()) != 0 {
		t.Fatal("approved agent prevented deletion or was evicted first")
	}
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "delete" {
			t.Fatal("disposable agent used Kubernetes DELETE")
		}
	}
}

func TestDisposableDaemonSetApprovalIsExact(t *testing.T) {
	for _, change := range []string{"unapproved", "wrong owner UID", "missing owner", "not controller", "mirror",
		"static", "finalizer", "preStop", "PVC", "NFS", "debug container", "template drift", "injected container", "storage drift", "terminating"} {
		t.Run(change, func(t *testing.T) {
			f, pod, ds := disposableFixture(t)
			switch change {
			case "unapproved":
				f.cfg.DisposableDaemonSets = nil
			case "wrong owner UID":
				pod.OwnerReferences[0].UID = "recreated"
			case "missing owner":
				if err := f.kube.AppsV1().DaemonSets(ds.Namespace).Delete(context.Background(), ds.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
			case "not controller":
				pod.OwnerReferences[0].Controller = ptr.To(false)
			case "mirror":
				pod.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "hash"}
			case "static":
				pod.Annotations = map[string]string{"kubernetes.io/config.source": "file"}
			case "finalizer":
				pod.Finalizers = []string{"protect"}
			case "preStop":
				pod.Spec.Containers[0].Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{"save-data"}}}}
			case "PVC":
				pod.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}
			case "NFS":
				pod.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{NFS: &corev1.NFSVolumeSource{Server: "storage", Path: "/data"}}}}
			case "debug container":
				pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{{}}
			case "template drift":
				ds.Spec.Template.Spec.Containers[0].Image = "agent:v2"
				if _, err := f.kube.AppsV1().DaemonSets(ds.Namespace).Update(context.Background(), ds, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			case "injected container":
				pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: "unexpected"})
			case "storage drift":
				pod.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/data"}}}}
			case "terminating":
				now := metav1.NewTime(f.now)
				pod.DeletionTimestamp = &now
			}
			if change == "preStop" || change == "PVC" || change == "NFS" || change == "debug container" {
				digest, err := DisposablePodSpecHash(pod)
				if err != nil {
					t.Fatal(err)
				}
				f.cfg.DisposableDaemonSets[0].PodSpecSHA256 = digest
			}
			if err := f.controller.disposablePod(context.Background(), pod, f.cfg); err == nil {
				t.Fatal("unapproved or protected pod was accepted")
			}
		})
	}
}

func TestDisposableDigestNormalizesBindingNotPermissions(t *testing.T) {
	pod := placementPod("agent", "node-00")
	pod.Spec.Volumes = []corev1.Volume{{Name: "kube-api-access-aaaaa", VolumeSource: corev1.VolumeSource{
		Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{
			ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token", Audience: "api"},
		}}},
	}}}
	pod.Spec.Containers = []corev1.Container{{Name: "agent", Image: "agent:v1",
		VolumeMounts: []corev1.VolumeMount{{Name: "kube-api-access-aaaaa", MountPath: "/token", ReadOnly: true}}}}
	pod.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
			MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"node-00"}}},
		}}},
	}}
	first, err := DisposablePodSpecHash(pod)
	if err != nil {
		t.Fatal(err)
	}
	copy := pod.DeepCopy()
	copy.Spec.NodeName = "node-01"
	copy.Spec.Volumes[0].Name = "kube-api-access-bbbbb"
	copy.Spec.Containers[0].VolumeMounts[0].Name = "kube-api-access-bbbbb"
	copy.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields[0].Values[0] = "node-01"
	second, err := DisposablePodSpecHash(copy)
	if err != nil || first != second {
		t.Fatalf("equivalent per-node spec changed digest: %v", err)
	}
	if pod.Spec.NodeName != "node-00" || pod.Spec.Volumes[0].Name != "kube-api-access-aaaaa" {
		t.Fatal("hash mutated the live Pod")
	}
	copy.Spec.Volumes[0].Projected.Sources[0].ServiceAccountToken.Audience = "different"
	changed, err := DisposablePodSpecHash(copy)
	if err != nil || changed == first {
		t.Fatal("normalization hid changed token permissions")
	}
}

func TestDaemonSetPolicyValidation(t *testing.T) {
	for _, change := range []string{"namespace", "name", "UID", "role", "template hash", "pod hash", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			f, _, _ := disposableFixture(t)
			p := &f.cfg.DisposableDaemonSets[0]
			switch change {
			case "namespace":
				p.Namespace = "*"
			case "name":
				p.Name = ""
			case "UID":
				p.UID = ""
			case "role":
				p.Role = "application"
			case "template hash":
				p.TemplateSHA256 = ""
			case "pod hash":
				p.PodSpecSHA256 = strings.Repeat("A", 64)
			case "duplicate":
				f.cfg.DisposableDaemonSets = append(f.cfg.DisposableDaemonSets, *p)
			}
			if err := f.cfg.Validate(); err == nil {
				t.Fatal("invalid disposable policy accepted")
			}
		})
	}
}
