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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

func placementNode(name, zone string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name), Labels: map[string]string{
			corev1.LabelTopologyZone: zone, corev1.LabelHostname: name,
		}},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("8Gi"),
				corev1.ResourcePods: resource.MustParse("10"), kuberesources.SwiftNICResourceName: resource.MustParse("2"),
			},
		},
	}
}

func placementPod(name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test", UID: types.UID(name), Labels: map[string]string{"app": "router"}},
		Spec: corev1.PodSpec{NodeName: node, TerminationGracePeriodSeconds: ptr.To(int64(30)),
			Containers: []corev1.Container{{Name: "router", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("1"),
			}}}},
		},
	}
}

func TestPlacementJointCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ClusterSnapshot, *corev1.Pod)
		held   bool
	}{
		{name: "available capacity"},
		{name: "old placement excluded from anti affinity", change: func(s *ClusterSnapshot, p *corev1.Pod) {
			p.Spec.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					TopologyKey: corev1.LabelTopologyZone, LabelSelector: &metav1.LabelSelector{MatchLabels: p.Labels},
				}},
			}}
		}},
		{name: "existing NIC allocation charged once", change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Pods = append(s.Pods, placementPod("resident", "destination"))
			s.NICs["destination"] = map[types.UID]int64{"resident": 1}
		}},
		{name: "extra NIC allocation", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Pods = append(s.Pods, placementPod("resident", "destination"))
			s.NICs["destination"] = map[types.UID]int64{"resident": 2}
		}},
		{name: "terminal pod still owns NICs", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			resident := placementPod("resident", "destination")
			resident.Status.Phase = corev1.PodSucceeded
			s.Pods = append(s.Pods, resident)
			s.NICs["destination"] = map[types.UID]int64{"resident": 2}
		}},
		{name: "UID on another node does not hide NICs", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.NICs["destination"] = map[types.UID]int64{p.UID: 2}
		}},
		{name: "pending demand also fits", change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Pods = append(s.Pods, placementPod("pending", ""))
		}},
		{name: "pending demand exhausts NICs", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Pods = append(s.Pods, placementPod("pending-1", ""), placementPod("pending-2", ""))
		}},
		{name: "pod slots exhausted", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Nodes[1].Status.Allocatable[corev1.ResourcePods] = resource.MustParse("0")
		}},
		{name: "untolerated taint", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Nodes[1].Spec.Taints = []corev1.Taint{{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}}
		}},
		{name: "required node selector", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			p.Spec.NodeSelector = map[string]string{"missing": "label"}
		}},
		{name: "externally cordoned capacity", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Nodes[1].Spec.Unschedulable = true
		}},
		{name: "hard topology skew", held: true, change: func(s *ClusterSnapshot, p *corev1.Pod) {
			s.Nodes[1].Labels[corev1.LabelTopologyZone] = "b"
			s.Pods = append(s.Pods, placementPod("resident", "destination"))
			p.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
				MaxSkew: 1, TopologyKey: corev1.LabelTopologyZone, WhenUnsatisfiable: corev1.DoNotSchedule,
				LabelSelector: &metav1.LabelSelector{MatchLabels: p.Labels},
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := placementPod("moving", "source")
			snapshot := ClusterSnapshot{
				Nodes: []*corev1.Node{placementNode("source", "a"), placementNode("destination", "a")},
				Pods:  []*corev1.Pod{pod}, NICs: map[string]map[types.UID]int64{},
			}
			if tc.change != nil {
				tc.change(&snapshot, pod)
			}
			err := placement(snapshot, map[string]bool{"source": true}, []*corev1.Pod{pod})
			if (err != nil) != tc.held {
				t.Fatalf("held=%v, error=%v", tc.held, err)
			}
		})
	}
}

func TestSpreadMinimumDomainsAndRevision(t *testing.T) {
	pod := placementPod("moving", "")
	pod.Labels["revision"] = "new"
	a, b := placementNode("a", "a"), placementNode("b", "b")
	resident := placementPod("old", "a")
	resident.Labels["revision"] = "old"
	constraint := corev1.TopologySpreadConstraint{
		MaxSkew: 1, TopologyKey: corev1.LabelTopologyZone, WhenUnsatisfiable: corev1.DoNotSchedule,
		LabelSelector:  &metav1.LabelSelector{MatchLabels: map[string]string{"app": "router"}},
		MatchLabelKeys: []string{"revision"}, MinDomains: ptr.To(int32(3)),
	}
	nodes := map[string]*corev1.Node{"a": a, "b": b}
	if ok, err := spreadFits(pod, a, constraint, []*corev1.Pod{resident}, nodes); err != nil || !ok {
		t.Fatalf("old revision must not consume skew: %v", err)
	}
	resident.Labels["revision"] = "new"
	if ok, err := spreadFits(pod, a, constraint, []*corev1.Pod{resident}, nodes); err != nil || ok {
		t.Fatalf("minDomains must use a zero global minimum: %v", err)
	}
}

func TestRequestsIncludeRestartableInitAndOverhead(t *testing.T) {
	pod := placementPod("moving", "")
	pod.Spec.InitContainers = []corev1.Container{
		{Name: "sidecar", RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3")}}},
		{Name: "init", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}}},
	}
	pod.Spec.Overhead = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}
	cpu := requests(pod)[corev1.ResourceCPU]
	if cpu.Cmp(resource.MustParse("8")) != 0 {
		t.Fatalf("expected 8 CPUs, got %s", cpu.String())
	}
}
