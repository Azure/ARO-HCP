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
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/utils"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

func (c *Controller) condition(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, kind string, status metav1.ConditionStatus, reason, message string) error {
	utils.LoggerFromContext(ctx).Info("mitigation observation", "episode", episode.Name, "condition", kind, "status", status, "reason", reason, "message", message)
	changed := meta.SetStatusCondition(&episode.Status.Conditions, metav1.Condition{
		Type: kind, Status: status, Reason: reason, Message: message,
		ObservedGeneration: episode.Generation, LastTransitionTime: metav1.NewTime(c.clock()),
	})
	if cfg.Mode != Enforce || !changed {
		return nil
	}
	return c.saveEpisode(ctx, revision, episode)
}

func (c *Controller) observe(ctx context.Context, cfg, accepted Config, revision uint64, episode *api.MitigationEpisode, budget *api.NodeMitigationBudget, snapshot ClusterSnapshot) error {
	if episode.Status.NodeDeletedAt == nil && cfg.Mode == Enforce {
		now := metav1.NewTime(c.clock())
		if reservation, found := budget.Status.Reservations[episode.Name]; found {
			// A delayed intent cannot age an actual deletion out of its window.
			reservation.DeleteStartedAt = &now
			budget.Status.Reservations[episode.Name] = reservation
			if err := c.saveBudget(ctx, revision, budget); err != nil {
				return err
			}
		}
		episode.Status.NodeDeletedAt = &now
		episode.Status.Intent = nil
		episode.Status.Phase = PhaseObserve
		meta.RemoveStatusCondition(&episode.Status.Conditions, "Held")
		return c.condition(ctx, cfg, revision, episode, "NodeObjectDeleted", metav1.ConditionTrue, "OriginalUIDAbsent", "Original Kubernetes Node UID is absent")
	}
	if episode.Status.NodeDeletedAt == nil {
		if err := c.condition(ctx, cfg, revision, episode, "NodeObjectDeleted", metav1.ConditionTrue, "OriginalUIDAbsent", "Original Kubernetes Node UID is absent"); err != nil {
			return err
		}
	}
	identity, present, err := c.azure.Instance(ctx, accepted.ClusterResourceID, poolFromID(episode.Spec.PoolID), episode.Spec.ProviderID, "")
	if err != nil {
		instanceVerificationFailures.Inc()
		return c.condition(ctx, cfg, revision, episode, "InstanceVerificationUnavailable", metav1.ConditionTrue, "ReadFailed", err.Error())
	}
	if err := c.condition(ctx, cfg, revision, episode, "InstanceVerificationUnavailable", metav1.ConditionFalse, "ReadSucceeded", "Original instance lookup succeeded"); err != nil {
		return err
	}
	if present && identity == episode.Spec.InstanceID {
		if err := c.condition(ctx, cfg, revision, episode, "InstanceGone", metav1.ConditionFalse, "OriginalInstancePresent", "Original immutable Azure instance is still present"); err != nil {
			return err
		}
		warningSince := episode.Status.NodeDeletedAt
		reason, message := "InstanceStillPresent", "Original instance remains 30 minutes after Kubernetes Node deletion"
		if action := episode.Status.NodeDeletionAction; action != nil && action.LastAttemptAt != nil &&
			(warningSince == nil || warningSince.Sub(action.LastAttemptAt.Time) > accepted.ObservationMaxAge.Duration) {
			warningSince = action.LastAttemptAt
			reason, message = "InstancePresentAfterDeletionAttempt", "Original Node UID is absent and its instance remains more than 30 minutes after the saved deletion attempt"
		}
		if warningSince != nil && c.clock().Sub(warningSince.Time) >= 30*time.Minute {
			instanceCleanupStalled.Inc()
			warn := !meta.IsStatusConditionTrue(episode.Status.Conditions, "InstanceCleanupStalled")
			if err := c.condition(ctx, cfg, revision, episode, "InstanceCleanupStalled", metav1.ConditionTrue, reason, message); err != nil {
				return err
			}
			if warn && cfg.Mode == Enforce {
				if err := c.event(ctx, revision, episode, corev1.EventTypeWarning, "InstanceCleanupStalled", "Original instance remains after the cleanup warning threshold; operator investigation is required"); err != nil {
					return err
				}
			}
		}
		registered := false
		for _, node := range snapshot.Nodes {
			if !strings.EqualFold(node.Status.NodeInfo.SystemUUID, episode.Spec.InstanceID) {
				continue
			}
			registered = true
			liveIdentity, exists, err := c.azure.Instance(ctx, accepted.ClusterResourceID, poolFromID(episode.Spec.PoolID), node.Spec.ProviderID, node.Name)
			if err != nil {
				return c.hold(ctx, cfg, revision, episode, err.Error())
			}
			if !exists || liveIdentity != episode.Spec.InstanceID {
				return c.hold(ctx, cfg, revision, episode, "re-registration identity is unverified")
			}
			if cfg.Mode != Enforce || cfg.ClusterResourceID != accepted.ClusterResourceID || !slices.Contains(cfg.Mitigators, episode.Spec.Mitigator) {
				return c.hold(ctx, cfg, revision, episode, "original instance re-registered while mitigation is paused")
			}
			reservation, found := budget.Status.Reservations[episode.Name]
			if !found || reservation.EpisodeUID != episode.UID || reservation.ReleasedAt != nil {
				return c.hold(ctx, cfg, revision, episode, "re-registration has no active reservation")
			}
			if node.Spec.Unschedulable && node.Annotations[ownershipAnnotation] == string(episode.UID) {
				if err := c.drainRegistration(ctx, cfg, revision, episode, budget, node, snapshot); err != nil {
					return err
				}
				continue
			}
			if episode.Status.Intent == nil {
				return c.prepare(ctx, revision, episode, "Cordon", node.ObjectMeta, episode.Status.Recovery, PhaseObserve)
			}
			if episode.Status.Intent.Kind != "Cordon" || episode.Status.Intent.UID != node.UID {
				episode.Status.Intent = nil
				return c.saveEpisode(ctx, revision, episode)
			}
			return c.execute(ctx, cfg, revision, episode, budget, node, nil)
		}
		if !registered {
			return c.clearHold(ctx, cfg, revision, episode)
		}
		return nil
	}
	if err := c.condition(ctx, cfg, revision, episode, "InstanceGone", metav1.ConditionTrue, "OriginalInstanceAbsent", "Original immutable Azure instance is absent"); err != nil {
		return err
	}
	if err := c.condition(ctx, cfg, revision, episode, "InstanceCleanupStalled", metav1.ConditionFalse, "OriginalInstanceAbsent", "Original instance no longer needs cleanup"); err != nil {
		return err
	}
	pool, err := c.azure.Pool(ctx, accepted.ClusterResourceID, poolFromID(episode.Spec.PoolID))
	if err != nil {
		return c.hold(ctx, cfg, revision, episode, err.Error())
	}
	available := 0
	seen := map[string]bool{}
	for _, node := range snapshot.Nodes {
		identity := strings.ToLower(node.Status.NodeInfo.SystemUUID)
		if identity == "" || seen[identity] {
			continue
		}
		seen[identity] = true
		if poolName(node) == poolFromID(episode.Spec.PoolID) && ready(node) && !node.Spec.Unschedulable && !snapshot.Faulted[node.Name] &&
			!strings.EqualFold(node.Status.NodeInfo.SystemUUID, episode.Spec.InstanceID) {
			available++
		}
	}
	if !pool.Stable || available < int(pool.Target) {
		return c.condition(ctx, cfg, revision, episode, "CapacityRestored", metav1.ConditionFalse, "WaitingForCapacity", "Ready schedulable pool capacity has not reached the target")
	}
	if episode.Status.Recovery != nil {
		ok, err := c.recovered(ctx, episode.Status.Recovery, episode.Spec.InstanceID)
		if err != nil {
			return err
		}
		if !ok {
			return c.hold(ctx, cfg, revision, episode, "workload recovery is not confirmed")
		}
	}
	if err := c.condition(ctx, cfg, revision, episode, "CapacityRestored", metav1.ConditionTrue, "TargetReady", "Ready schedulable pool capacity meets the target"); err != nil {
		return err
	}
	if cfg.Mode != Enforce {
		return nil
	}
	if reservation, exists := budget.Status.Reservations[episode.Name]; exists {
		if reservation.EpisodeUID != episode.UID {
			return fmt.Errorf("cannot release another episode's reservation")
		}
		if reservation.ReleasedAt == nil {
			now := metav1.NewTime(c.clock())
			reservation.ReleasedAt = &now
			budget.Status.Reservations[episode.Name] = reservation
			if err := c.saveBudget(ctx, revision, budget); err != nil {
				return err
			}
		}
	}
	now := metav1.NewTime(c.clock())
	episode.Status.CompletedAt = &now
	episode.Status.Intent = nil
	episode.Status.Recovery = nil
	return c.phase(ctx, revision, episode, PhaseComplete)
}

func (c *Controller) retainOrPrune(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, budget *api.NodeMitigationBudget) error {
	if cfg.Mode != Enforce || episode.Status.CompletedAt == nil ||
		c.clock().Sub(episode.Status.CompletedAt.Time) < budget.Status.Window.Duration {
		return nil
	}
	if reservation, exists := budget.Status.Reservations[episode.Name]; exists {
		if reservation.ReleasedAt == nil {
			return fmt.Errorf("completed episode still has an active reservation")
		}
		if reservation.DeleteStartedAt != nil && c.clock().Sub(reservation.DeleteStartedAt.Time) < budget.Status.Window.Duration {
			return nil
		}
		delete(budget.Status.Reservations, episode.Name)
		if err := c.saveBudget(ctx, revision, budget); err != nil {
			return err
		}
	}
	return c.write(revision, func() error {
		return c.records.MgmtagentV1alpha1().MitigationEpisodes(c.namespace).Delete(ctx, episode.Name, metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &episode.UID, ResourceVersion: &episode.ResourceVersion},
		})
	})
}

func (c *Controller) drainRegistration(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, budget *api.NodeMitigationBudget, node *corev1.Node, snapshot ClusterSnapshot) error {
	if episode.Status.Intent != nil && episode.Status.Intent.Kind == "Cordon" {
		return c.execute(ctx, cfg, revision, episode, budget, node, nil)
	}
	pods := []*corev1.Pod{}
	for _, pod := range snapshot.Pods {
		if pod.Spec.NodeName == node.Name && !terminal(pod) {
			pods = append(pods, pod)
		}
	}
	if len(pods) == 0 && episode.Status.Intent == nil && episode.Status.Recovery == nil {
		return c.clearHold(ctx, cfg, revision, episode)
	}
	if _, _, err := c.admission(ctx, cfg, revision, node, budget, snapshot, episode.Name); err != nil {
		return c.hold(ctx, cfg, revision, episode, err.Error())
	}
	_, events := nodeEvidence(node, pods, snapshot.Events, c.clock())
	if episode.Status.Intent != nil {
		if episode.Status.Intent.Kind != "EvictPod" && episode.Status.Intent.Kind != "DeleteUnhealthyPod" {
			return c.hold(ctx, cfg, revision, episode, "only cordon and pod actions may target a re-registration")
		}
		return c.execute(ctx, cfg, revision, episode, budget, node, events)
	}
	if recovery := episode.Status.Recovery; recovery != nil {
		recovered, err := c.recovered(ctx, recovery, episode.Spec.InstanceID)
		if err != nil {
			return err
		}
		if recovered {
			episode.Status.Recovery = nil
			meta.RemoveStatusCondition(&episode.Status.Conditions, "Held")
			return c.saveEpisode(ctx, revision, episode)
		}
		found := false
		for _, pod := range pods {
			if pod.UID == recovery.PodUID {
				found = true
			}
		}
		if !found {
			return c.hold(ctx, cfg, revision, episode, "waiting for workload recovery after re-registration")
		}
	}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			return c.hold(ctx, cfg, revision, episode, "waiting for pod termination on re-registration")
		}
		permitted, err := permittedDaemonSet(ctx, c.kube, pod, cfg)
		if err != nil {
			return c.hold(ctx, cfg, revision, episode, err.Error())
		}
		if permitted {
			continue
		}
		if _, _, stalled := detectors.SwiftSandboxStalled(pod, events, c.clock()); stalled && cfg.Rescue {
			return c.preparePod(ctx, cfg, revision, episode, pod, PhaseRescue)
		}
		reservation := budget.Status.Reservations[episode.Name]
		if !cfg.Drain || c.clock().Sub(reservation.ReservedAt.Time) < cfg.CleanupDelay.Duration {
			return c.hold(ctx, cfg, revision, episode, "re-registration drain is disabled or delayed")
		}
		return c.preparePod(ctx, cfg, revision, episode, pod, PhaseDrain)
	}
	return c.clearHold(ctx, cfg, revision, episode)
}
