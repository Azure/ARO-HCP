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
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/internal/utils"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

func (c *Controller) hold(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, reason string) error {
	utils.LoggerFromContext(ctx).Info("mitigation held", "episode", episode.Name, "reason", reason, "mode", cfg.Mode)
	if cfg.Mode != Enforce {
		return nil
	}
	condition := metav1.Condition{
		Type: "Held", Status: metav1.ConditionTrue, Reason: "SafetyGate",
		Message: reason, ObservedGeneration: episode.Generation, LastTransitionTime: metav1.NewTime(c.clock()),
	}
	if !meta.SetStatusCondition(&episode.Status.Conditions, condition) {
		return nil
	}
	return c.saveEpisode(ctx, revision, episode)
}

func (c *Controller) clearHold(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode) error {
	if cfg.Mode != Enforce || !meta.IsStatusConditionTrue(episode.Status.Conditions, "Held") {
		return nil
	}
	meta.RemoveStatusCondition(&episode.Status.Conditions, "Held")
	return c.saveEpisode(ctx, revision, episode)
}

func (c *Controller) phase(ctx context.Context, revision uint64, episode *api.MitigationEpisode, next string) error {
	episode.Status.Phase = next
	meta.RemoveStatusCondition(&episode.Status.Conditions, "Held")
	return c.saveEpisode(ctx, revision, episode)
}

func (c *Controller) reconcileEpisode(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, budget *api.NodeMitigationBudget, snapshot ClusterSnapshot) error {
	var accepted Config
	if err := json.Unmarshal([]byte(episode.Spec.Policy), &accepted); err != nil {
		return fmt.Errorf("invalid saved policy: %w", err)
	}
	if err := accepted.Validate(); err != nil {
		return err
	}
	cfg.hasAcceptedPolicy = true
	cfg.acceptedWorkloads = accepted.Workloads
	cfg.acceptedDaemonSets = accepted.DaemonSets
	if episode.Status.Phase == PhaseComplete {
		return c.retainOrPrune(ctx, cfg, revision, episode, budget)
	}
	if reservation, exists := budget.Status.Reservations[episode.Name]; exists && reservation.ReleasedAt != nil {
		if reservation.EpisodeUID != episode.UID {
			return fmt.Errorf("episode reservation identity mismatch")
		}
		if cfg.Mode != Enforce {
			return nil
		}
		episode.Status.CompletedAt = reservation.ReleasedAt.DeepCopy()
		episode.Status.Intent = nil
		return c.phase(ctx, revision, episode, PhaseComplete)
	}
	node, err := c.kube.CoreV1().Nodes().Get(ctx, episode.Spec.NodeName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	absent := apierrors.IsNotFound(err) || node.UID != episode.Spec.NodeUID
	if _, reserved := budget.Status.Reservations[episode.Name]; !reserved && cfg.Mode != Disabled &&
		(episode.Status.CurrentNodeUID != "" || episode.Status.Intent != nil || episode.Status.NodeDeletedAt != nil) {
		var observationErr error
		if absent || episode.Status.NodeDeletedAt != nil {
			observer := cfg
			observer.Mode = Disabled
			observationErr = c.observe(ctx, observer, accepted, revision, episode, budget, snapshot)
		}
		return errors.Join(observationErr, c.hold(ctx, cfg, revision, episode, "active reservation is missing; restore accounting before further action"))
	}
	if absent || episode.Status.NodeDeletedAt != nil {
		return c.observe(ctx, cfg, accepted, revision, episode, budget, snapshot)
	}
	if cfg.Mode == Disabled {
		utils.LoggerFromContext(ctx).Info("observing paused mitigation", "episode", episode.Name, "phase", episode.Status.Phase, "mode", cfg.Mode)
		return nil
	}
	if cfg.ClusterResourceID != accepted.ClusterResourceID || !slices.Contains(cfg.Mitigators, episode.Spec.Mitigator) {
		return c.hold(ctx, cfg, revision, episode, "accepted cluster or mitigator is not enabled")
	}
	if node.Spec.ProviderID != episode.Spec.ProviderID || !strings.EqualFold(node.Status.NodeInfo.SystemUUID, episode.Spec.InstanceID) {
		return c.hold(ctx, cfg, revision, episode, "original node instance identity changed")
	}
	detections, events := nodeEvidence(node, snapshot.Pods, snapshot.Events, c.clock())
	var detection detectors.Detection
	for _, candidate := range detections {
		if candidate.Detector == episode.Spec.Detector {
			detection = candidate
			break
		}
	}
	reservation, hasReservation := budget.Status.Reservations[episode.Name]
	if !hasReservation && detection.Detector == "" {
		if cfg.Mode == Audit {
			return c.hold(ctx, cfg, revision, episode, "unreserved candidate no longer has fault evidence")
		}
		// No reservation means admission never completed and no external action
		// was permitted. This is not an undo of an accepted cleanup plan.
		now := metav1.NewTime(c.clock())
		episode.Status.CompletedAt = &now
		return c.phase(ctx, revision, episode, PhaseComplete)
	}
	if hasReservation && reservation.EpisodeUID != episode.UID {
		return fmt.Errorf("episode reservation identity mismatch")
	}
	if episode.Spec.Mitigator == "never-ready" && ready(node) {
		return c.cancelNeverReady(ctx, cfg, revision, episode, budget, node)
	}
	if episode.Spec.Mitigator == "swift" && node.Spec.Unschedulable &&
		node.Annotations[ownershipAnnotation] != string(episode.UID) {
		return c.hold(ctx, cfg, revision, episode, "external cordon is not mitigation ownership")
	}
	fresh := snapshot
	_, instance, err := c.admission(ctx, cfg, revision, node, budget, fresh, episode.Name)
	if err != nil {
		return c.hold(ctx, cfg, revision, episode, err.Error())
	}
	if instance != episode.Spec.InstanceID {
		return c.hold(ctx, cfg, revision, episode, "immutable instance identity changed")
	}
	mitigator := c.routes[episode.Spec.Detector]
	if mitigator == nil || mitigator.Name() != episode.Spec.Mitigator {
		return c.hold(ctx, cfg, revision, episode, "unknown saved mitigation route")
	}
	decision, err := mitigator.Plan(Input{Node: node, Detection: detection, Episode: episode, Policy: cfg, Now: c.clock()})
	if err != nil {
		return err
	}
	if decision.Hold != "" {
		return c.hold(ctx, cfg, revision, episode, decision.Hold)
	}
	var rescuePod *corev1.Pod
	if cfg.Rescue && episode.Status.Intent == nil && episode.Status.Recovery == nil &&
		(decision.Phase == PhaseRescue || decision.Phase == PhaseDrain) {
		for _, pod := range fresh.Pods {
			if pod.Spec.NodeName != node.Name || pod.DeletionTimestamp != nil || !kuberesources.PodRequestsSwiftNIC(pod) {
				continue
			}
			if _, _, stalled := detectors.SwiftSandboxStalled(pod, events, c.clock()); stalled {
				rescuePod, decision.Phase = pod, PhaseRescue
				break
			}
		}
	}
	if cfg.Mode == Audit {
		eligible := actionEnabled(cfg, decision.Phase)
		auditPhase := decision.Phase
		reason := "independent eligibility; no simulated action progress"
		if episode.Status.Intent != nil {
			auditPhase = episode.Status.Intent.Phase
			err := c.execute(ctx, cfg, revision, episode, budget, node, events)
			eligible = errors.Is(err, errAuditEligible)
			if !eligible {
				reason = "prepared action requires recovery, refreshed intent, or a safety gate"
				if err != nil && !errors.Is(err, ErrPaused) {
					return err
				}
			}
		} else if episode.Status.Recovery != nil {
			recovered, err := c.recovered(ctx, episode.Status.Recovery, episode.Spec.InstanceID)
			if err != nil {
				return err
			}
			eligible = false
			reason = "waiting for workload recovery"
			if recovered {
				reason = "recovery observed; persisted phase progression is paused"
			}
		}
		c.logCandidate(ctx, node, cfg, episode.Spec.Detector, auditPhase, eligible, reason)
		return nil
	}
	if !hasReservation {
		return c.reserve(ctx, revision, episode, budget)
	}
	if episode.Status.Intent != nil {
		return c.execute(ctx, cfg, revision, episode, budget, node, events)
	}
	if episode.Status.Recovery != nil {
		recovered, err := c.recovered(ctx, episode.Status.Recovery, episode.Spec.InstanceID)
		if err != nil {
			return err
		}
		if !recovered {
			return c.hold(ctx, cfg, revision, episode, "waiting for actual workload recovery")
		}
		episode.Status.Recovery = nil
		meta.RemoveStatusCondition(&episode.Status.Conditions, "Held")
		return c.saveEpisode(ctx, revision, episode)
	}
	switch decision.Phase {
	case PhaseCordon:
		if !cfg.Rescue {
			return c.hold(ctx, cfg, revision, episode, "rescue phase disabled")
		}
		if node.Spec.Unschedulable && node.Annotations[ownershipAnnotation] == string(episode.UID) {
			return c.phase(ctx, revision, episode, PhaseRescue)
		}
		return c.prepare(ctx, revision, episode, "Cordon", node.ObjectMeta, nil, PhaseCordon)
	case PhaseRescue:
		if !cfg.Rescue {
			return c.hold(ctx, cfg, revision, episode, "rescue phase disabled")
		}
		if rescuePod != nil {
			return c.preparePod(ctx, cfg, revision, episode, rescuePod, PhaseRescue)
		}
		return c.phase(ctx, revision, episode, PhaseDrain)
	case PhaseDrain:
		if !cfg.Drain {
			return c.hold(ctx, cfg, revision, episode, "drain phase disabled")
		}
		if c.clock().Sub(reservation.ReservedAt.Time) < cfg.CleanupDelay.Duration {
			return c.hold(ctx, cfg, revision, episode, "cleanup delay has not elapsed")
		}
		for _, pod := range fresh.Pods {
			if pod.Spec.NodeName != node.Name || terminal(pod) {
				continue
			}
			permitted, err := permittedDaemonSet(ctx, c.kube, pod, cfg)
			if err != nil {
				return c.hold(ctx, cfg, revision, episode, err.Error())
			}
			if permitted {
				continue
			}
			if pod.DeletionTimestamp != nil {
				return c.hold(ctx, cfg, revision, episode, "waiting for pod termination")
			}
			return c.preparePod(ctx, cfg, revision, episode, pod, PhaseDrain)
		}
		return c.phase(ctx, revision, episode, PhaseDelete)
	case PhaseDelete:
		if !cfg.DeleteNode {
			return c.hold(ctx, cfg, revision, episode, "Node deletion disabled")
		}
		if err := c.safeToDeleteNode(ctx, cfg, node); err != nil {
			return c.hold(ctx, cfg, revision, episode, err.Error())
		}
		if reservation.DeleteStartedAt == nil {
			t := metav1.NewTime(c.clock())
			reservation.DeleteStartedAt = &t
			budget.Status.Reservations[episode.Name] = reservation
			return c.saveBudget(ctx, revision, budget)
		}
		return c.prepare(ctx, revision, episode, "DeleteNode", node.ObjectMeta, nil, PhaseDelete)
	default:
		return fmt.Errorf("unsupported phase %q for live original Node", decision.Phase)
	}
}

func (c *Controller) cancelNeverReady(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, budget *api.NodeMitigationBudget, node *corev1.Node) error {
	intent := episode.Status.Intent
	if node.DeletionTimestamp != nil || (intent != nil && intent.LastAttemptAt != nil && node.ResourceVersion == intent.ResourceVersion) {
		return c.hold(ctx, cfg, revision, episode, "submitted deletion outcome is not fenced by a new Node resourceVersion")
	}
	if cfg.Mode != Enforce {
		return c.hold(ctx, cfg, revision, episode, "never-ready candidate recovered")
	}
	now := metav1.NewTime(c.clock())
	if reservation, found := budget.Status.Reservations[episode.Name]; found {
		reservation.ReleasedAt = &now
		reservation.DeleteStartedAt = nil
		budget.Status.Reservations[episode.Name] = reservation
		if err := c.saveBudget(ctx, revision, budget); err != nil {
			return err
		}
	}
	episode.Status.Intent = nil
	episode.Status.CompletedAt = &now
	return c.phase(ctx, revision, episode, PhaseComplete)
}

func (c *Controller) prepare(ctx context.Context, revision uint64, episode *api.MitigationEpisode, kind string, object metav1.ObjectMeta, recovery *api.WorkloadRecovery, phase string) error {
	meta.RemoveStatusCondition(&episode.Status.Conditions, "Held")
	episode.Status.Intent = &api.MitigationAction{
		ID:   episodeName(string(episode.UID) + kind + string(object.UID) + object.ResourceVersion),
		Kind: kind, Phase: phase, Namespace: object.Namespace, Name: object.Name, UID: object.UID, ResourceVersion: object.ResourceVersion,
		StartedAt: metav1.NewTime(c.clock()),
	}
	if episode.Status.CurrentNodeUID == "" {
		episode.Status.CurrentNodeUID = episode.Spec.NodeUID
	}
	if kind == "Cordon" {
		episode.Status.CurrentNodeUID = object.UID
	}
	episode.Status.Recovery = recovery
	return c.saveEpisode(ctx, revision, episode)
}

func (c *Controller) preparePod(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, pod *corev1.Pod, phase string) error {
	deployment, _, err := workload(ctx, c.kube, pod, cfg)
	if err != nil {
		return c.hold(ctx, cfg, revision, episode, err.Error())
	}
	pods, err := c.kube.CoreV1().Pods(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	if len(pods.Items) > maxRecords {
		return c.hold(ctx, cfg, revision, episode, "workload recovery observation exceeds the record limit")
	}
	existing := make([]types.UID, 0, len(pods.Items))
	for _, item := range pods.Items {
		existing = append(existing, item.UID)
	}
	available, err := c.availablePods(ctx, deployment, pods.Items, episode.Spec.InstanceID)
	if err != nil {
		return err
	}
	required := min(*deployment.Spec.Replicas, int32(len(available))+1)
	return c.prepare(ctx, revision, episode, "EvictPod", pod.ObjectMeta, &api.WorkloadRecovery{
		StartedAt: metav1.NewTime(c.clock()),
		Namespace: pod.Namespace, Deployment: deployment.Name, DeploymentUID: deployment.UID,
		PodUID: pod.UID, Replicas: required, ExistingPodUIDs: existing,
	}, phase)
}

func (c *Controller) safeToDeleteNode(ctx context.Context, cfg Config, node *corev1.Node) error {
	pods, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node.Name})
	if err != nil {
		return err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != node.Name {
			continue
		}
		if terminal(pod) && len(pod.Finalizers) == 0 {
			continue
		}
		// A NotReady node is not proof that any process has stopped.
		if !ready(node) {
			return fmt.Errorf("nonterminal workload remains on a NotReady node")
		}
		permitted, err := permittedDaemonSet(ctx, c.kube, pod, cfg)
		if err != nil {
			return err
		}
		if !permitted {
			return fmt.Errorf("workload remains before Node deletion")
		}
	}
	if len(node.Finalizers) > 0 {
		return fmt.Errorf("node finalizers require operator resolution")
	}
	return nil
}

func (c *Controller) recovered(ctx context.Context, recovery *api.WorkloadRecovery, excludedInstance string) (recovered bool, err error) {
	defer func() {
		outcome := "waiting"
		if recovered {
			outcome = "ready"
		}
		if err != nil {
			outcome = "unavailable"
		}
		recoveryObservations.WithLabelValues(outcome).Inc()
	}()
	deployment, err := c.kube.AppsV1().Deployments(recovery.Namespace).Get(ctx, recovery.Deployment, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if deployment.UID != recovery.DeploymentUID || deployment.DeletionTimestamp != nil ||
		deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < recovery.Replicas ||
		deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.AvailableReplicas < recovery.Replicas {
		return false, nil
	}
	pods, err := c.kube.CoreV1().Pods(recovery.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	for i := range pods.Items {
		if pods.Items[i].UID == recovery.PodUID && !terminal(&pods.Items[i]) {
			return false, nil
		}
	}
	available, err := c.availablePods(ctx, deployment, pods.Items, excludedInstance)
	if err != nil {
		return false, err
	}
	newReady := false
	for _, pod := range available {
		if !slices.Contains(recovery.ExistingPodUIDs, pod.UID) {
			newReady = true
		}
	}
	return newReady && int32(len(available)) >= recovery.Replicas, nil
}

func (c *Controller) availablePods(ctx context.Context, deployment *appsv1.Deployment, pods []corev1.Pod, excludedInstance string) ([]*corev1.Pod, error) {
	replicas, err := c.kube.AppsV1().ReplicaSets(deployment.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	owners := map[types.UID]bool{}
	for _, rs := range replicas.Items {
		owner := metav1.GetControllerOf(&rs)
		if owner != nil && owner.APIVersion == "apps/v1" && owner.Kind == "Deployment" &&
			owner.UID == deployment.UID && rs.DeletionTimestamp == nil {
			owners[rs.UID] = true
		}
	}
	nodes := map[string]*corev1.Node{}
	var available []*corev1.Pod
	for i := range pods {
		pod := &pods[i]
		owner := metav1.GetControllerOf(pod)
		if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "ReplicaSet" ||
			!owners[owner.UID] || !podReady(pod) || pod.Spec.NodeName == "" {
			continue
		}
		if deployment.Spec.MinReadySeconds > 0 {
			stable := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && !condition.LastTransitionTime.IsZero() &&
					c.clock().Sub(condition.LastTransitionTime.Time) >= time.Duration(deployment.Spec.MinReadySeconds)*time.Second {
					stable = true
				}
			}
			if !stable {
				continue
			}
		}
		node := nodes[pod.Spec.NodeName]
		if node == nil {
			node, err = c.kube.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			nodes[node.Name] = node
		}
		if ready(node) && !node.Spec.Unschedulable && node.Status.NodeInfo.SystemUUID != "" &&
			!strings.EqualFold(node.Status.NodeInfo.SystemUUID, excludedInstance) {
			available = append(available, pod)
		}
	}
	return available, nil
}
