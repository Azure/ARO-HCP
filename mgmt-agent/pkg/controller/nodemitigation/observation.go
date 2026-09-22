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
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/utils"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

func (c *Controller) deleteCandidate(ctx context.Context, cfg Config, revision uint64, node *corev1.Node,
	budget *api.NodeMitigationBudget, snapshot ClusterSnapshot) (bool, error) {
	key := recordName(string(node.UID))
	if _, found := budget.Status.Reservations[key]; found {
		return false, nil
	}
	live, err := c.kube.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if live.UID != node.UID || live.Spec.ProviderID != node.Spec.ProviderID ||
		live.Spec.Unschedulable || live.DeletionTimestamp != nil ||
		live.Labels[ownershipLabel] != "" || live.Annotations[cordonAnnotation] != "" {
		return false, fmt.Errorf("candidate identity, cordon or ownership changed")
	}
	node = live
	if err := c.checkNeverReady(node); err != nil {
		return false, err
	}
	observation, instance, err := c.admission(ctx, cfg, revision, node, budget, snapshot, "")
	if err != nil {
		return false, err
	}
	machine, err := c.azure.Machine(ctx, cfg.ClusterResourceID, poolName(node), node.Spec.ProviderID)
	if err != nil {
		return false, err
	}
	if _, err := ownershipPatch(node.ObjectMeta); err != nil {
		return false, err
	}
	if len(budget.Status.Reservations) >= maxRecords {
		return false, fmt.Errorf("deletion reservation limit reached")
	}
	c.logCandidate(ctx, node, cfg, "never-ready", ActionDelete, true, "")
	if cfg.Mode == Audit {
		return false, nil
	}
	r := api.MitigationReservation{NodeName: node.Name, NodeUID: node.UID, ProviderID: node.Spec.ProviderID,
		InstanceID: instance, PoolID: observation.ID, MachineName: machine, Zone: node.Labels[corev1.LabelTopologyZone]}
	if err := c.reserveDeletion(ctx, revision, r, budget); err != nil {
		return false, err
	}
	return true, c.claimCordon(ctx, revision, node, budget.Status.Reservations[key])
}

func reservationCluster(pool string) (string, error) {
	index := strings.LastIndex(strings.ToLower(pool), "/agentpools/")
	if index < 1 {
		return "", fmt.Errorf("invalid reservation pool identity")
	}
	return pool[:index], nil
}

func (c *Controller) deletionTarget(ctx context.Context, cfg Config, r api.MitigationReservation) (*corev1.Node, error) {
	cluster, err := reservationCluster(r.PoolID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(cluster, cfg.ClusterResourceID) || !slices.Contains(cfg.Mitigators, "never-ready") {
		return nil, fmt.Errorf("deletion target is not authorized by current configuration")
	}
	node, err := c.kube.CoreV1().Nodes().Get(ctx, r.NodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if node.UID != r.NodeUID || node.Spec.ProviderID != r.ProviderID ||
		!strings.EqualFold(node.Status.NodeInfo.SystemUUID, r.InstanceID) ||
		!node.Spec.Unschedulable || !ownsCordon(node, r) ||
		node.DeletionTimestamp != nil || poolName(node) != poolFromID(r.PoolID) {
		return nil, fmt.Errorf("deletion target identity or scheduling state changed")
	}
	if err := c.checkNeverReady(node); err != nil {
		return nil, err
	}
	decision, fault := detectors.Decide(node, nil, nil, c.clock())
	if decision != detectors.DecisionWedged || fault.DetectorName != "never-ready" {
		return nil, fmt.Errorf("node no longer meets the never-ready criterion")
	}
	pods, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node.Name})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName == node.Name {
			if err := c.disposablePod(ctx, &pods.Items[i], cfg); err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}

func (c *Controller) submitDeletion(ctx context.Context, cfg Config, revision uint64, key string,
	budget *api.NodeMitigationBudget, snapshot ClusterSnapshot) error {
	r := budget.Status.Reservations[key]
	if r.DeleteStartedAt != nil || r.ReleasedAt != nil || r.OperationToken != "" || r.Outcome != "" {
		return fmt.Errorf("deletion request already attempted; resubmission is not safe")
	}
	node, err := c.deletionTarget(ctx, cfg, r)
	if err != nil {
		return c.cancelReservation(ctx, revision, key, budget, err)
	}
	_, instance, err := c.admission(ctx, cfg, revision, node, budget, snapshot, key)
	if err != nil {
		return err
	}
	if instance != r.InstanceID {
		return c.cancelReservation(ctx, revision, key, budget, fmt.Errorf("original instance identity changed"))
	}
	machine, err := c.azure.Machine(ctx, cfg.ClusterResourceID, poolName(node), r.ProviderID)
	if err != nil {
		return err
	}
	if machine != r.MachineName {
		return c.cancelReservation(ctx, revision, key, budget, fmt.Errorf("AKS machine identity changed"))
	}
	identity, exists, err := c.azure.Instance(ctx, cfg.ClusterResourceID, poolName(node), r.ProviderID, r.NodeName)
	if err != nil {
		return err
	}
	if !exists || identity != r.InstanceID {
		return c.cancelReservation(ctx, revision, key, budget, fmt.Errorf("original machine no longer exists"))
	}
	if _, err := c.deletionTarget(ctx, cfg, r); err != nil {
		return c.cancelReservation(ctx, revision, key, budget, err)
	}
	if snapshot.ObservedAt.After(c.clock()) || c.clock().Sub(snapshot.ObservedAt) > cfg.ObservationMaxAge.Duration {
		return fmt.Errorf("cluster snapshot expired before AKS submission")
	}
	now := metav1.NewTime(c.clock())
	r.DeleteStartedAt, r.Outcome = &now, "Unknown"
	budget.Status.Reservations[key] = r
	if err := c.saveBudget(ctx, revision, budget); err != nil {
		return err
	}
	var operation MachineOperation
	err = c.action(revision, ActionDelete, func() error {
		var err error
		operation, err = c.azure.DeleteMachine(ctx, r.PoolID, r.MachineName)
		return err
	})
	if err != nil {
		// Even a lost acceptance response consumes allowance and forbids replay.
		return err
	}
	if err := c.recordOperation(ctx, cfg, revision, key, budget, operation); err != nil {
		return err
	}
	return c.event(ctx, revision, corev1.ObjectReference{APIVersion: "v1", Kind: "Node",
		Name: r.NodeName, UID: r.NodeUID}, "MachineDeletionAccepted", "AKS accepted the machine-deletion request")
}

func (c *Controller) cancelReservation(ctx context.Context, revision uint64, key string, budget *api.NodeMitigationBudget, reason error) error {
	r := budget.Status.Reservations[key]
	if r.DeleteStartedAt != nil {
		return fmt.Errorf("cannot cancel an attempted deletion: %w", reason)
	}
	if err := c.releaseCordon(ctx, revision, r); err != nil {
		return errors.Join(reason, err)
	}
	now := metav1.NewTime(c.clock())
	r.ReleasedAt, r.Outcome, r.Message = &now, "Cancelled", reason.Error()
	budget.Status.Reservations[key] = r
	if err := c.saveBudget(ctx, revision, budget); err != nil {
		return err
	}
	utils.LoggerFromContext(ctx).Info("deletion reservation cancelled", "nodeUID", r.NodeUID, "reason", reason)
	return nil
}

func (c *Controller) recordOperation(ctx context.Context, cfg Config, revision uint64, key string,
	budget *api.NodeMitigationBudget, op MachineOperation) error {
	if op.Outcome != "Pending" && op.Outcome != "Succeeded" && op.Outcome != "Failed" {
		return fmt.Errorf("unrecognized AKS operation outcome %q", op.Outcome)
	}
	if op.Outcome == "Pending" && op.Token == "" {
		return fmt.Errorf("pending AKS operation has no resume token")
	}
	if len(op.Token) > 65536 {
		return fmt.Errorf("AKS operation reference exceeds storage limit")
	}
	r := budget.Status.Reservations[key]
	utils.LoggerFromContext(ctx).Info("AKS deletion outcome", "pool", r.PoolID, "machine", r.MachineName,
		"nodeUID", r.NodeUID, "outcome", op.Outcome, "message", op.Message)
	operationResults.WithLabelValues(op.Outcome).Inc()
	c.nextPoll[key] = op.PollAfter
	if cfg.Mode != Enforce {
		return nil
	}
	message := op.Message
	if len(message) > 4096 {
		message = message[:4096]
	}
	r.Outcome, r.OperationToken, r.Message = op.Outcome, op.Token, message
	r.PollAfter = nil
	if !op.PollAfter.IsZero() {
		t := metav1.NewTime(op.PollAfter)
		r.PollAfter = &t
	}
	budget.Status.Reservations[key] = r
	return c.saveBudget(ctx, revision, budget)
}

func (c *Controller) observeDeletion(ctx context.Context, cfg Config, revision uint64, key string,
	budget *api.NodeMitigationBudget, snapshot ClusterSnapshot, capacityObserved bool) error {
	r := budget.Status.Reservations[key]
	if r.DeleteStartedAt == nil {
		if cfg.Mode == Enforce && capacityObserved {
			return c.submitDeletion(ctx, cfg, revision, key, budget, snapshot)
		}
		return fmt.Errorf("unsubmitted deletion is paused; cordon release requires enforce mode and current observations")
	}
	if r.Outcome == "Unknown" || r.Outcome == "" {
		return fmt.Errorf("AKS deletion outcome unknown for %s; reservation retained, operator reconciliation required", r.NodeUID)
	}
	if r.Outcome == "Pending" {
		if c.nextPoll[key].After(c.clock()) || (r.PollAfter != nil && r.PollAfter.After(c.clock())) {
			return nil
		}
		op, err := c.azure.PollDeletion(ctx, r.PoolID, r.OperationToken)
		if err != nil {
			c.nextPoll[key] = op.PollAfter
			if cfg.Mode == Enforce && !op.PollAfter.IsZero() {
				deadline := metav1.NewTime(op.PollAfter)
				r.PollAfter = &deadline
				budget.Status.Reservations[key] = r
				return errors.Join(err, c.saveBudget(ctx, revision, budget))
			}
			return err
		}
		if err := c.recordOperation(ctx, cfg, revision, key, budget, op); err != nil {
			return err
		}
		r = budget.Status.Reservations[key]
		if cfg.Mode != Enforce {
			r.Outcome = op.Outcome
		}
	}
	if r.Outcome != "Succeeded" && r.Outcome != "Failed" {
		return nil
	}
	if !capacityObserved {
		return nil
	}
	cluster, err := reservationCluster(r.PoolID)
	if err != nil {
		return err
	}
	pool, err := c.azure.Pool(ctx, cluster, poolFromID(r.PoolID))
	if err != nil {
		return err
	}
	maxAge := cfg.ObservationMaxAge.Duration
	if cfg.Mode == Disabled {
		maxAge = cfg.retryInterval()
	}
	if !strings.EqualFold(pool.ID, r.PoolID) || pool.ObservedAt.After(c.clock()) ||
		c.clock().Sub(pool.ObservedAt) > maxAge || snapshot.ObservedAt.After(c.clock()) ||
		c.clock().Sub(snapshot.ObservedAt) > maxAge {
		return fmt.Errorf("pool observation unavailable or stale")
	}
	reserved := map[string]bool{}
	for _, other := range budget.Status.Reservations {
		if other.ReleasedAt == nil {
			reserved[strings.ToLower(other.InstanceID)] = true
		}
	}
	seen := map[string]bool{}
	healthyPool, healthyZone := 0, 0
	for _, node := range snapshot.Nodes {
		id := strings.ToLower(node.Status.NodeInfo.SystemUUID)
		if id == "" || seen[id] {
			return fmt.Errorf("missing or duplicate instance identity in capacity observations")
		}
		seen[id] = true
		if !ready(node) || node.Spec.Unschedulable || snapshot.Faulted[node.Name] || reserved[id] {
			continue
		}
		if poolName(node) == poolFromID(r.PoolID) {
			healthyPool++
		}
		if node.Labels[corev1.LabelTopologyZone] == r.Zone {
			healthyZone++
		}
	}
	poolCapacity.WithLabelValues(poolFromID(r.PoolID), "target").Set(float64(pool.Target))
	poolCapacity.WithLabelValues(poolFromID(r.PoolID), "ready").Set(float64(healthyPool))
	if !pool.Stable || healthyPool < int(pool.Target) || healthyPool < cfg.MinHealthyPool ||
		healthyZone < cfg.MinHealthyZone || cfg.Mode != Enforce {
		return nil
	}
	now := metav1.NewTime(c.clock())
	r.ReleasedAt = &now
	budget.Status.Reservations[key] = r
	return c.saveBudget(ctx, revision, budget)
}
