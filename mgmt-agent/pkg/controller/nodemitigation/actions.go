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
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/Azure/ARO-HCP/internal/utils"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const ownershipLabel = "node-mitigation.aro-hcp.azure.com/managed-by"

func ownershipPatch(meta metav1.ObjectMeta) ([]byte, error) {
	if owner := meta.Labels[ownershipLabel]; owner != "" && owner != ControllerName {
		return nil, fmt.Errorf("resource has another mitigation owner")
	}
	labels := make(map[string]string, len(meta.Labels)+1)
	for key, value := range meta.Labels {
		labels[key] = value
	}
	labels[ownershipLabel] = ControllerName
	return json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": meta.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": meta.ResourceVersion},
		{"op": "add", "path": "/metadata/labels", "value": labels},
	})
}

func (c *Controller) availableWorkload(ctx context.Context, pod *corev1.Pod, cfg Config, snapshot ClusterSnapshot, excluded map[string]bool) (*appsv1.Deployment, error) {
	deployment, policy, err := workload(ctx, c.kube, pod, cfg)
	if err != nil {
		return nil, err
	}
	if policy.MinAvailableReplicas == nil {
		return nil, fmt.Errorf("workload availability floor missing")
	}
	pods, err := c.kube.CoreV1().Pods(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	healthyNodes := map[string]bool{}
	for _, node := range snapshot.Nodes {
		healthyNodes[node.Name] = ready(node) && !excluded[node.Name]
	}
	replicaSets := map[types.UID]bool{}
	available := int32(0)
	for i := range pods.Items {
		other := &pods.Items[i]
		owner := metav1.GetControllerOf(other)
		if !podReady(other) || !healthyNodes[other.Spec.NodeName] || owner == nil ||
			owner.Kind != "ReplicaSet" || owner.APIVersion != "apps/v1" {
			continue
		}
		belongs, known := replicaSets[owner.UID]
		if !known {
			rs, err := c.kube.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			parent := metav1.GetControllerOf(rs)
			belongs = rs.UID == owner.UID && rs.DeletionTimestamp == nil && parent != nil &&
				parent.Kind == "Deployment" && parent.APIVersion == "apps/v1" && parent.UID == deployment.UID
			replicaSets[owner.UID] = belongs
		}
		if belongs {
			available++
		}
	}
	available = min(available, deployment.Status.AvailableReplicas)
	workloadAvailability.WithLabelValues("observed").Set(float64(available))
	if available < *policy.MinAvailableReplicas {
		return nil, fmt.Errorf("workload availability %d is below floor %d", available, *policy.MinAvailableReplicas)
	}
	return deployment, nil
}

func (c *Controller) evictionTarget(ctx context.Context, cfg Config, original *corev1.Node, selected *corev1.Pod,
	snapshot ClusterSnapshot, excluded map[string]bool) (*corev1.Pod, *appsv1.Deployment, error) {
	if err := snapshot.checkFreshness(c.clock(), cfg.ObservationMaxAge.Duration); err != nil {
		return nil, nil, err
	}
	node, err := c.kube.CoreV1().Nodes().Get(ctx, original.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if node.UID != original.UID || node.Spec.ProviderID != original.Spec.ProviderID ||
		node.Status.NodeInfo.SystemUUID != original.Status.NodeInfo.SystemUUID ||
		!ready(node) || node.Spec.Unschedulable {
		return nil, nil, fmt.Errorf("source Node identity or readiness changed")
	}
	pod, err := c.kube.CoreV1().Pods(selected.Namespace).Get(ctx, selected.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if pod.UID != selected.UID || pod.Spec.NodeName != node.Name || pod.DeletionTimestamp != nil || terminal(pod) || podReady(pod) {
		return nil, nil, fmt.Errorf("pod no longer needs rescue or its identity changed")
	}
	events, err := c.kube.CoreV1().Events(pod.Namespace).List(ctx,
		metav1.ListOptions{FieldSelector: "involvedObject.uid=" + string(pod.UID)})
	if err != nil {
		return nil, nil, err
	}
	detections := nodeEvidence(node, []*corev1.Pod{pod}, events.Items, c.clock())
	if !slices.ContainsFunc(detections, func(d detectors.Detection) bool {
		return d.Detector == detectors.SwiftPodSandboxStalled && d.Scope == detectors.PodScope && slices.Contains(d.PodUIDs, pod.UID)
	}) {
		return nil, nil, fmt.Errorf("pod fault evidence expired or recovered")
	}
	deployment, err := c.availableWorkload(ctx, pod, cfg, snapshot, excluded)
	return pod, deployment, err
}

func (c *Controller) checkPlacement(cfg Config, snapshot ClusterSnapshot, pod *corev1.Pod, excluded map[string]bool) error {
	if err := snapshot.checkFreshness(c.clock(), cfg.ObservationMaxAge.Duration); err != nil {
		return err
	}
	snapshot.Pods = slices.Clone(snapshot.Pods)
	index := slices.IndexFunc(snapshot.Pods, func(p *corev1.Pod) bool { return p.UID == pod.UID })
	if index < 0 {
		return fmt.Errorf("admitted Pod is missing from the cluster snapshot")
	}
	snapshot.Pods[index] = pod
	faulted := make(map[string]bool, len(snapshot.Faulted))
	for name, value := range snapshot.Faulted {
		faulted[name] = name != pod.Spec.NodeName && value
	}
	snapshot.Faulted = faulted
	return placement(snapshot, excluded, []*corev1.Pod{pod})
}

func (c *Controller) rescue(ctx context.Context, cfg Config, revision uint64, node *corev1.Node,
	detection detectors.Detection, budget *api.NodeMitigationBudget, snapshot ClusterSnapshot) (bool, error) {
	excluded := map[string]bool{}
	for _, selected := range snapshot.Pods {
		if selected.Spec.NodeName != node.Name || !slices.Contains(detection.PodUIDs, selected.UID) {
			continue
		}
		pod, deployment, err := c.evictionTarget(ctx, cfg, node, selected, snapshot, excluded)
		if err != nil {
			return false, err
		}
		if err := evictionAllowance(budget.Status, cfg, deployment.UID, node.UID, c.clock()); err != nil {
			return false, err
		}
		if err := c.checkPlacement(cfg, snapshot, pod, excluded); err != nil {
			return false, err
		}
		patch, err := ownershipPatch(pod.ObjectMeta)
		if err != nil {
			return false, err
		}
		c.logCandidate(ctx, node, cfg, detection.Detector, ActionEvict, true, "")
		if cfg.Mode == Audit {
			return false, nil
		}
		if pod.Labels[ownershipLabel] != ControllerName {
			if err := c.write(revision, func() error {
				_, err := c.kube.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
				return err
			}); err != nil {
				return false, err
			}
		}
		ownerUID := deployment.UID
		snapshot, err = c.snapshot(ctx)
		if err != nil {
			return false, err
		}
		pod, deployment, err = c.evictionTarget(ctx, cfg, node, pod, snapshot, excluded)
		if err != nil {
			return false, err
		}
		if pod.Labels[ownershipLabel] != ControllerName {
			return false, fmt.Errorf("pod mitigation ownership changed during eviction admission")
		}
		if deployment.UID != ownerUID {
			return false, fmt.Errorf("workload owner changed during eviction admission")
		}
		if err := c.checkPlacement(cfg, snapshot, pod, excluded); err != nil {
			return false, err
		}
		id := string(uuid.NewUUID())
		budget.Status.EvictionWindow = cfg.EvictionWindow
		budget.Status.Evictions[id] = api.EvictionRecord{
			WorkloadUID: deployment.UID, NodeUID: node.UID, PodUID: pod.UID, AttemptedAt: metav1.NewTime(c.clock()),
		}
		if err := c.saveBudget(ctx, revision, budget); err != nil {
			return false, err
		}
		logger := utils.LoggerFromContext(ctx).WithValues("cluster", cfg.ClusterResourceID, "node", node.Name, "nodeUID", node.UID,
			"pod", pod.Name, "podUID", pod.UID, "namespace", pod.Namespace, "workloadUID", deployment.UID,
			"detector", detection.Detector, "attempt", id, "timestamp", c.clock())
		logger.Info("pod eviction requested")
		err = c.action(revision, ActionEvict, func() error {
			return c.kube.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
				DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{
					UID: &pod.UID, ResourceVersion: &pod.ResourceVersion,
				}},
			})
		})
		logger.Info("pod eviction result", "outcome", actionOutcome(err), "error", err)
		if err != nil {
			return true, err
		}
		return true, c.event(ctx, revision, corev1.ObjectReference{APIVersion: "v1", Kind: "Pod",
			Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID}, "PodEvictionAccepted", "Kubernetes accepted the failing pod eviction")
	}
	return false, fmt.Errorf("no matching live pod for SWIFT detection")
}

func (c *Controller) event(ctx context.Context, revision uint64, target corev1.ObjectReference, reason, message string) error {
	namespace := target.Namespace
	if namespace == "" {
		namespace = c.namespace
	}
	return c.write(revision, func() error {
		_, err := c.kube.CoreV1().Events(namespace).Create(ctx, &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{GenerateName: "node-mitigation-", Namespace: namespace},
			InvolvedObject: target, Type: corev1.EventTypeNormal, Reason: reason, Message: message,
			Source:         corev1.EventSource{Component: ControllerName},
			FirstTimestamp: metav1.NewTime(c.clock()), LastTimestamp: metav1.NewTime(c.clock()), Count: 1,
		}, metav1.CreateOptions{})
		return err
	})
}
