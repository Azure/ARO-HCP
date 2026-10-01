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

package swiftpod

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

var mtpncGVR = schema.GroupVersionResource{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "multitenantpodnetworkconfigs"}

type ClusterSnapshot struct {
	ObservedAt time.Time
	Nodes      []*corev1.Node
	Pods       []*corev1.Pod
	Events     []corev1.Event
	Namespaces map[string]*corev1.Namespace
	Faulted    map[string]bool
	Stalled    map[string][]types.UID
	// NICs includes allocations whose original pods have already disappeared.
	NICs map[string]map[types.UID]int64
}

func (s ClusterSnapshot) checkFreshness(now time.Time, maxAge time.Duration) error {
	if s.ObservedAt.After(now) || now.Sub(s.ObservedAt) > maxAge {
		return fmt.Errorf("cluster snapshot is stale")
	}
	return nil
}

// Cached discovery only selects work. Admission and placement use a
// live cluster-wide snapshot so missed watch updates cannot authorize disruption.
func (c *Controller) snapshot(ctx context.Context) (ClusterSnapshot, error) {
	snapshot := ClusterSnapshot{ObservedAt: c.clock(), Namespaces: map[string]*corev1.Namespace{}, NICs: map[string]map[types.UID]int64{}}
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, err
	}
	for i := range nodes.Items {
		snapshot.Nodes = append(snapshot.Nodes, &nodes.Items[i])
	}
	pods, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, err
	}
	podsByNode := map[string][]*corev1.Pod{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		snapshot.Pods = append(snapshot.Pods, pod)
		podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], pod)
	}
	events, err := c.kube.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.kind=Pod"})
	if err != nil {
		return snapshot, err
	}
	snapshot.Events = events.Items
	eventsByNode := map[string][]*corev1.Event{}
	for i := range snapshot.Events {
		event := &snapshot.Events[i]
		eventsByNode[event.Source.Host] = append(eventsByNode[event.Source.Host], event)
	}
	snapshot.Faulted = map[string]bool{}
	snapshot.Stalled = map[string][]types.UID{}
	for _, node := range snapshot.Nodes {
		onNode, onNodeEvents := podsByNode[node.Name], eventsByNode[node.Name]
		for _, pod := range onNode {
			if swiftNode(node) && stalled(pod, onNodeEvents, c.clock()) {
				snapshot.Stalled[node.Name] = append(snapshot.Stalled[node.Name], pod.UID)
			}
		}
		decision, _ := detectors.Decide(node, onNodeEvents, onNode, c.clock())
		snapshot.Faulted[node.Name] = decision == detectors.DecisionWedged
	}
	namespaces, err := c.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, err
	}
	for i := range namespaces.Items {
		ns := &namespaces.Items[i]
		snapshot.Namespaces[ns.Name] = ns
	}
	nics, err := c.dynamic.Resource(mtpncGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return snapshot, fmt.Errorf("read delegated NIC allocations: %w", err)
	}
	for _, nic := range nics.Items {
		node, found, err := unstructured.NestedString(nic.Object, "status", "nodeName")
		if err != nil {
			return snapshot, err
		}
		if !found || node == "" {
			return snapshot, fmt.Errorf("delegated NIC allocation has unknown node identity")
		}
		pod, found, err := unstructured.NestedString(nic.Object, "spec", "podUID")
		if err != nil || !found || pod == "" {
			return snapshot, fmt.Errorf("delegated NIC allocation has unknown pod identity")
		}
		interfaces, _, err := unstructured.NestedSlice(nic.Object, "status", "interfaceInfos")
		if err != nil {
			return snapshot, err
		}
		count := int64(max(1, len(interfaces)))
		if snapshot.NICs[node] == nil {
			snapshot.NICs[node] = map[types.UID]int64{}
		}
		snapshot.NICs[node][types.UID(pod)] += count
	}
	return snapshot, nil
}

func swiftNode(node *corev1.Node) bool {
	return ready(node) && node.Labels[detectors.SwiftV2LabelKey] == detectors.SwiftV2LabelValue
}
