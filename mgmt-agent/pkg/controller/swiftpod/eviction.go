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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func (c *Controller) evictionTarget(ctx context.Context, cfg Config, original *corev1.Node, selected *corev1.Pod,
	snapshot ClusterSnapshot) (*corev1.Pod, *appsv1.Deployment, error) {
	if err := snapshot.checkFreshness(c.clock(), cfg.ObservationMaxAge.Duration); err != nil {
		return nil, nil, err
	}
	node, err := c.kube.CoreV1().Nodes().Get(ctx, original.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if node.UID == "" || node.UID != original.UID || node.Spec.ProviderID != original.Spec.ProviderID ||
		node.Status.NodeInfo.SystemUUID != original.Status.NodeInfo.SystemUUID ||
		!swiftNode(node) || node.Spec.Unschedulable {
		return nil, nil, fmt.Errorf("source Node identity, applicability or readiness changed")
	}
	pod, err := c.kube.CoreV1().Pods(selected.Namespace).Get(ctx, selected.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	if pod.UID != selected.UID || pod.Spec.NodeName != node.Name || pod.DeletionTimestamp != nil || terminal(pod) || podReady(pod) {
		return nil, nil, fmt.Errorf("pod no longer needs rescue or its identity changed")
	}
	observed, err := c.kube.CoreV1().Events(pod.Namespace).List(ctx,
		metav1.ListOptions{FieldSelector: "involvedObject.uid=" + string(pod.UID)})
	if err != nil {
		return nil, nil, err
	}
	events := make([]*corev1.Event, 0, len(observed.Items))
	for i := range observed.Items {
		events = append(events, &observed.Items[i])
	}
	if !stalled(pod, events, c.clock()) {
		return nil, nil, fmt.Errorf("pod fault evidence expired or recovered")
	}
	deployment, err := c.availableWorkload(ctx, pod, cfg, snapshot, nil)
	return pod, deployment, err
}

func (c *Controller) rescue(ctx context.Context, cfg Config, revision uint64, node *corev1.Node, selected *corev1.Pod, snapshot ClusterSnapshot) error {
	pod, deployment, err := c.evictionTarget(ctx, cfg, node, selected, snapshot)
	if err != nil {
		return err
	}
	b, err := c.readBudget(ctx, cfg)
	if err != nil {
		return err
	}
	if err := evictionAllowance(b, cfg, deployment.UID, node.UID, c.clock()); err != nil {
		return err
	}
	if err := c.checkPlacement(cfg, snapshot, pod); err != nil {
		return err
	}
	patch, err := ownershipPatch(pod.ObjectMeta)
	if err != nil {
		return err
	}
	logger := utils.LoggerFromContext(ctx).WithValues("node", node.Name, "nodeUID", node.UID,
		"namespace", pod.Namespace, "pod", pod.Name, "podUID", pod.UID, "workloadUID", deployment.UID, "mode", cfg.Mode)
	c.mu.RLock()
	if revision != c.revision || c.config.Mode == Disabled {
		c.mu.RUnlock()
		return ErrPaused
	}
	logger.Info("SWIFT router candidate eligible")
	c.mu.RUnlock()
	if cfg.Mode == Audit {
		return nil
	}
	if pod.Labels[ownershipLabel] != ControllerName {
		if err := c.write(revision, func() error {
			_, err := c.kube.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	ownerUID := deployment.UID
	snapshot, err = c.snapshot(ctx)
	if err != nil {
		return err
	}
	pod, deployment, err = c.evictionTarget(ctx, cfg, node, pod, snapshot)
	if err != nil {
		return err
	}
	if pod.Labels[ownershipLabel] != ControllerName || deployment.UID != ownerUID {
		return fmt.Errorf("pod mitigation ownership or workload identity changed during admission")
	}
	if err := c.checkPlacement(cfg, snapshot, pod); err != nil {
		return err
	}
	// The ConfigMap resourceVersion makes competing admissions conflict. Keep
	// recording and submission inside one configuration fence.
	err = c.write(revision, func() error {
		if err := evictionAllowance(b, cfg, deployment.UID, node.UID, c.clock()); err != nil {
			return err
		}
		id := string(uuid.NewUUID())
		b.EvictionWindow = cfg.EvictionWindow
		b.Evictions[id] = evictionRecord{
			WorkloadUID: deployment.UID, NodeUID: node.UID, PodUID: pod.UID, AttemptedAt: metav1.NewTime(c.clock()),
		}
		if err := c.saveBudget(ctx, b); err != nil {
			return err
		}
		logger.Info("SWIFT router eviction requested", "attempt", id)
		err := c.kube.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
			DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{
				UID: &pod.UID, ResourceVersion: &pod.ResourceVersion,
			}},
		})
		logger.Info("SWIFT router eviction result", "attempt", id, "accepted", err == nil, "error", err)
		return err
	})
	if err != nil {
		return err
	}
	return c.write(revision, func() error {
		_, err := c.kube.CoreV1().Events(pod.Namespace).Create(ctx, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "swift-pod-mitigation-", Namespace: pod.Namespace},
			InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Pod",
				Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
			Type: corev1.EventTypeNormal, Reason: "PodEvictionAccepted", Message: "Kubernetes accepted the failing router Pod eviction",
			Source:         corev1.EventSource{Component: ControllerName},
			FirstTimestamp: metav1.NewTime(c.clock()), LastTimestamp: metav1.NewTime(c.clock()), Count: 1,
		}, metav1.CreateOptions{})
		return err
	})
}
