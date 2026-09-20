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

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const ownershipAnnotation = "node-mitigation.aro-hcp.azure.com/episode"

var errAuditEligible = errors.New("action eligible without audit writes")

func (c *Controller) execute(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, budget *api.NodeMitigationBudget, node *corev1.Node, events []*corev1.Event) error {
	intent := episode.Status.Intent
	if intent == nil {
		return fmt.Errorf("action has no durable intent")
	}
	if intent.LastAttemptAt != nil && c.clock().Sub(intent.LastAttemptAt.Time) < cfg.RetryInterval.Duration {
		return nil
	}
	switch intent.Kind {
	case "Cordon":
		if episode.Status.Phase != PhaseObserve && !cfg.Rescue {
			return c.hold(ctx, cfg, revision, episode, "rescue disabled")
		}
		current, err := c.kube.CoreV1().Nodes().Get(ctx, intent.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.UID != intent.UID || current.Spec.ProviderID != node.Spec.ProviderID ||
			!strings.EqualFold(current.Status.NodeInfo.SystemUUID, episode.Spec.InstanceID) {
			return c.hold(ctx, cfg, revision, episode, "cordon target identity changed")
		}
		if current.Spec.Unschedulable {
			if current.Annotations[ownershipAnnotation] != string(episode.UID) {
				return c.hold(ctx, cfg, revision, episode, "external cordon")
			}
			episode.Status.Intent = nil
			if episode.Status.Phase == PhaseObserve {
				return c.saveEpisode(ctx, revision, episode)
			}
			return c.phase(ctx, revision, episode, PhaseRescue)
		}
		if current.ResourceVersion != intent.ResourceVersion {
			intent.ResourceVersion = current.ResourceVersion
			if err := c.saveEpisode(ctx, revision, episode); err != nil {
				return err
			}
			intent = episode.Status.Intent
		}
		annotations := map[string]string{}
		for key, value := range current.Annotations {
			annotations[key] = value
		}
		annotations[ownershipAnnotation] = string(episode.UID)
		patch, err := json.Marshal([]map[string]any{
			{"op": "test", "path": "/metadata/uid", "value": intent.UID},
			{"op": "test", "path": "/metadata/resourceVersion", "value": intent.ResourceVersion},
			{"op": "add", "path": "/spec/unschedulable", "value": true},
			{"op": "add", "path": "/metadata/annotations", "value": annotations},
		})
		if err != nil {
			return err
		}
		if err := c.attempt(ctx, cfg, revision, episode); err != nil {
			return err
		}
		err = c.action(revision, "Cordon", func() error {
			_, err := c.kube.CoreV1().Nodes().Patch(ctx, intent.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
			return err
		})
		if err != nil {
			return err
		}
		return c.event(ctx, revision, episode, corev1.EventTypeNormal, "NodeCordoned", "Node cordoned under its mitigation reservation")
	case "DeleteNode":
		if !cfg.DeleteNode {
			return c.hold(ctx, cfg, revision, episode, "Node deletion disabled")
		}
		current, err := c.kube.CoreV1().Nodes().Get(ctx, intent.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.UID != intent.UID || current.Spec.ProviderID != episode.Spec.ProviderID ||
			!strings.EqualFold(current.Status.NodeInfo.SystemUUID, episode.Spec.InstanceID) {
			return c.hold(ctx, cfg, revision, episode, "Node DELETE identity changed")
		}
		node = current
		if node.DeletionTimestamp != nil {
			return nil
		}
		if episode.Spec.Mitigator == "swift" && (!node.Spec.Unschedulable || node.Annotations[ownershipAnnotation] != string(episode.UID)) {
			return c.hold(ctx, cfg, revision, episode, "Node deletion requires the owned cordon")
		}
		if episode.Spec.Mitigator == "never-ready" {
			decision, snapshot := detectors.Decide(node, nil, nil, c.clock())
			if decision != detectors.DecisionWedged || snapshot.DetectorName != "never-ready" {
				return c.hold(ctx, cfg, revision, episode, "never-ready evidence no longer valid")
			}
		}
		if err := c.safeToDeleteNode(ctx, cfg, node); err != nil {
			return c.hold(ctx, cfg, revision, episode, err.Error())
		}
		if node.ResourceVersion != intent.ResourceVersion {
			intent.ResourceVersion = node.ResourceVersion
			if err := c.saveEpisode(ctx, revision, episode); err != nil {
				return err
			}
			intent = episode.Status.Intent
		}
		reservation, found := budget.Status.Reservations[episode.Name]
		if !found || reservation.DeleteStartedAt == nil || reservation.ReleasedAt != nil {
			return fmt.Errorf("node deletion has no active deletion reservation")
		}
		if c.clock().Sub(reservation.ReservedAt.Time) < cfg.CleanupDelay.Duration {
			return c.hold(ctx, cfg, revision, episode, "cleanup delay has not elapsed")
		}
		if err := c.attempt(ctx, cfg, revision, episode); err != nil {
			return err
		}
		if err := c.action(revision, "DeleteNode", func() error {
			return c.kube.CoreV1().Nodes().Delete(ctx, intent.Name, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &intent.UID, ResourceVersion: &intent.ResourceVersion},
			})
		}); err != nil {
			return err
		}
		return c.event(ctx, revision, episode, corev1.EventTypeNormal, "NodeDeleteSubmitted", "Kubernetes Node deletion submitted; instance cleanup is not yet confirmed")
	case "EvictPod", "DeleteUnhealthyPod":
		return c.executePod(ctx, cfg, revision, episode, node, events)
	default:
		return fmt.Errorf("unsupported action %q", intent.Kind)
	}
}

func (c *Controller) attempt(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode) error {
	if cfg.Mode == Audit {
		return errAuditEligible
	}
	t := metav1.NewTime(c.clock())
	episode.Status.Intent.LastAttemptAt = &t
	if episode.Status.Intent.Kind == "DeleteNode" {
		episode.Status.NodeDeletionAction = episode.Status.Intent.DeepCopy()
	}
	return c.saveEpisode(ctx, revision, episode)
}

func (c *Controller) executePod(ctx context.Context, cfg Config, revision uint64, episode *api.MitigationEpisode, node *corev1.Node, events []*corev1.Event) error {
	intent := episode.Status.Intent
	current, err := c.kube.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if current.UID != node.UID || current.Spec.ProviderID != node.Spec.ProviderID ||
		!strings.EqualFold(current.Status.NodeInfo.SystemUUID, episode.Spec.InstanceID) {
		return c.hold(ctx, cfg, revision, episode, "pod source Node identity changed")
	}
	node = current
	if (intent.Phase == PhaseRescue && !cfg.Rescue) || (intent.Phase == PhaseDrain && !cfg.Drain) {
		return c.hold(ctx, cfg, revision, episode, "pod action phase disabled")
	}
	if intent.Phase != PhaseRescue && intent.Phase != PhaseDrain {
		return fmt.Errorf("invalid pod action phase %q", intent.Phase)
	}
	if !ready(node) {
		return c.hold(ctx, cfg, revision, episode, "pod process safety requires a Ready source Node")
	}
	pod, err := c.kube.CoreV1().Pods(intent.Namespace).Get(ctx, intent.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) || (err == nil && pod.UID != intent.UID) {
		episode.Status.Intent = nil
		return c.saveEpisode(ctx, revision, episode)
	}
	if err != nil {
		return err
	}
	if pod.Spec.NodeName != node.Name || !node.Spec.Unschedulable || node.Annotations[ownershipAnnotation] != string(episode.UID) {
		return c.hold(ctx, cfg, revision, episode, "pod or cordon ownership changed")
	}
	if pod.DeletionTimestamp != nil {
		return nil
	}
	if terminal(pod) {
		episode.Status.Intent = nil
		episode.Status.Recovery = nil
		return c.saveEpisode(ctx, revision, episode)
	}
	_, policy, err := workload(ctx, c.kube, pod, cfg)
	if err != nil {
		return c.hold(ctx, cfg, revision, episode, err.Error())
	}
	_, _, stalled := detectors.SwiftSandboxStalled(pod, events, c.clock())
	if intent.Phase == PhaseRescue && !stalled {
		episode.Status.Intent = nil
		episode.Status.Recovery = nil
		return c.saveEpisode(ctx, revision, episode)
	}
	if pod.ResourceVersion != intent.ResourceVersion {
		intent.ResourceVersion = pod.ResourceVersion
		// A changed object needs a new eviction attempt, not a stale bypass.
		intent.Kind = "EvictPod"
		intent.PDBName, intent.PDBResourceVersion, intent.PDBUID = "", "", ""
		if err := c.saveEpisode(ctx, revision, episode); err != nil {
			return err
		}
		intent = episode.Status.Intent
	}
	options := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &intent.UID, ResourceVersion: &intent.ResourceVersion}}
	if intent.Kind == "DeleteUnhealthyPod" {
		if !policy.AllowUnhealthyDeletion || !stalled || podReady(pod) {
			return c.hold(ctx, cfg, revision, episode, "unhealthy-pod bypass no longer authorized")
		}
		pdb, err := c.blockingPDB(ctx, pod)
		if err != nil {
			return c.hold(ctx, cfg, revision, episode, err.Error())
		}
		if pdb.UID != intent.PDBUID || pdb.ResourceVersion != intent.PDBResourceVersion {
			intent.Kind = "EvictPod"
			return c.saveEpisode(ctx, revision, episode)
		}
		if err := c.attempt(ctx, cfg, revision, episode); err != nil {
			return err
		}
		if err := c.action(revision, "DeleteUnhealthyPod", func() error { return c.kube.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options) }); err != nil {
			return err
		}
		return c.event(ctx, revision, episode, corev1.EventTypeWarning, "UnhealthyPodPDBBypass", "Explicit workload policy allowed graceful deletion after confirmed PDB denial")
	}
	if err := c.attempt(ctx, cfg, revision, episode); err != nil {
		return err
	}
	err = c.action(revision, "EvictPod", func() error {
		return c.kube.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
			ObjectMeta:    metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
			DeleteOptions: &options,
		})
	})
	if err == nil {
		return c.event(ctx, revision, episode, corev1.EventTypeNormal, "PodEvictionSubmitted", "Pod eviction submitted; waiting for termination and replacement readiness")
	}
	if !pdbDenial(err) || !policy.AllowUnhealthyDeletion || !stalled {
		return err
	}
	pdb, pdbErr := c.blockingPDB(ctx, pod)
	if pdbErr != nil {
		return pdbErr
	}
	intent = episode.Status.Intent
	intent.Kind = "DeleteUnhealthyPod"
	intent.ID = episodeName(string(episode.UID) + intent.Kind + string(pod.UID) + pod.ResourceVersion)
	intent.PDBName, intent.PDBUID, intent.PDBResourceVersion = pdb.Name, pdb.UID, pdb.ResourceVersion
	return c.saveEpisode(ctx, revision, episode)
}

func pdbDenial(err error) bool {
	if !apierrors.IsTooManyRequests(err) {
		return false
	}
	status, ok := err.(apierrors.APIStatus)
	if !ok || status.Status().Details == nil {
		return false
	}
	for _, cause := range status.Status().Details.Causes {
		if cause.Type == policyv1.DisruptionBudgetCause {
			return true
		}
	}
	return false
}

func (c *Controller) blockingPDB(ctx context.Context, pod *corev1.Pod) (*policyv1.PodDisruptionBudget, error) {
	budgets, err := c.kube.PolicyV1().PodDisruptionBudgets(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var matching *policyv1.PodDisruptionBudget
	for i := range budgets.Items {
		pdb := &budgets.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			return nil, err
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		if matching != nil {
			return nil, fmt.Errorf("overlapping PDBs do not authorize bypass")
		}
		matching = pdb
	}
	if matching == nil || matching.DeletionTimestamp != nil || matching.Status.ObservedGeneration < matching.Generation ||
		matching.Status.DisruptionsAllowed > 0 || (matching.Spec.UnhealthyPodEvictionPolicy != nil && *matching.Spec.UnhealthyPodEvictionPolicy == policyv1.AlwaysAllow) {
		return nil, fmt.Errorf("no current blocking PDB")
	}
	return matching, nil
}

func (c *Controller) event(ctx context.Context, revision uint64, episode *api.MitigationEpisode, eventType, reason, message string) error {
	return c.write(revision, func() error {
		_, err := c.kube.CoreV1().Events(c.namespace).Create(ctx, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "node-mitigation-", Namespace: c.namespace},
			InvolvedObject: corev1.ObjectReference{
				APIVersion: api.SchemeGroupVersion.String(), Kind: "MitigationEpisode",
				Name: episode.Name, Namespace: c.namespace, UID: episode.UID,
			},
			Type: eventType, Reason: reason, Message: message, Source: corev1.EventSource{Component: ControllerName},
			FirstTimestamp: metav1.NewTime(c.clock()), LastTimestamp: metav1.NewTime(c.clock()), Count: 1,
		}, metav1.CreateOptions{})
		return err
	})
}
