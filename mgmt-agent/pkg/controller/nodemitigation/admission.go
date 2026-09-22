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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func (c *Controller) admission(ctx context.Context, cfg Config, revision uint64, node *corev1.Node,
	budget *api.NodeMitigationBudget, snapshot ClusterSnapshot, own string) (PoolObservation, string, error) {
	if snapshot.ObservedAt.After(c.clock()) || c.clock().Sub(snapshot.ObservedAt) > cfg.ObservationMaxAge.Duration {
		return PoolObservation{}, "", fmt.Errorf("cluster snapshot is stale")
	}
	observation, err := c.azure.Pool(ctx, cfg.ClusterResourceID, poolName(node))
	if err != nil {
		return observation, "", err
	}
	instance, exists, err := c.azure.Instance(ctx, cfg.ClusterResourceID, poolName(node), node.Spec.ProviderID, node.Name)
	if err != nil {
		return observation, "", err
	}
	if !exists || instance == "" || !strings.EqualFold(instance, node.Status.NodeInfo.SystemUUID) {
		return observation, "", fmt.Errorf("node and Azure instance identity do not match")
	}
	for _, n := range snapshot.Nodes {
		if n.UID != node.UID && strings.EqualFold(n.Status.NodeInfo.SystemUUID, instance) {
			return observation, "", fmt.Errorf("another Node already represents this instance")
		}
		if poolName(n) == poolName(node) && ready(n) && !n.Spec.Unschedulable && !snapshot.Faulted[n.Name] {
			observation.Ready++
		}
	}
	var previous *api.PoolBaseline
	if saved, found := budget.Status.Pools[observation.ID]; found {
		previous = &saved
	} else if len(budget.Status.Pools) >= 128 {
		return observation, "", fmt.Errorf("pool baseline storage limit reached")
	}
	baseline, err := updateBaseline(previous, observation, cfg, c.observer, c.clock())
	if err != nil {
		return observation, "", err
	}
	if budget.Status.Window.Duration != 0 && budget.Status.Window.Duration != cfg.Window.Duration && len(budget.Status.Reservations) > 0 {
		return observation, "", fmt.Errorf("deletion-window change requires an explicit accounting migration")
	}
	budget.Status.Window = cfg.Window
	budget.Status.Pools[observation.ID] = baseline
	if cfg.Mode == Enforce {
		if err := c.saveBudget(ctx, revision, budget); err != nil {
			return observation, "", err
		}
	}
	if !observation.Stable {
		return observation, "", fmt.Errorf("agent-pool operation in progress")
	}
	if allowance(budget.Status, observation.ID, own, c.clock(), cfg.Window.Duration) < 1 {
		return observation, "", fmt.Errorf("rolling deletion allowance exhausted")
	}
	_, err = capacityLimits(snapshot, budget.Status, node, cfg, baseline.Size)
	if err != nil {
		return observation, "", err
	}
	for _, pod := range snapshot.Pods {
		if pod.Spec.NodeName == node.Name {
			if err := c.disposablePod(ctx, pod, cfg); err != nil {
				return observation, "", err
			}
		}
	}
	return observation, instance, nil
}

func capacityLimits(snapshot ClusterSnapshot, budget api.NodeMitigationBudgetStatus, target *corev1.Node, cfg Config, baseline int32) (map[string]bool, error) {
	if target.Labels[corev1.LabelTopologyZone] == "" || poolName(target) == "" {
		return nil, fmt.Errorf("node pool or zone identity missing")
	}
	type location struct{ pool, zone string }
	unavailable := map[string]location{}
	reserved := map[string]bool{}
	reserved[strings.ToLower(target.Status.NodeInfo.SystemUUID)] = true
	for _, reservation := range budget.Reservations {
		if reservation.ReleasedAt != nil {
			continue
		}
		id := strings.ToLower(reservation.InstanceID)
		reserved[id] = true
		unavailable[id] = location{poolFromID(reservation.PoolID), reservation.Zone}
	}
	excluded := map[string]bool{target.Name: true}
	healthyPool, healthyZone := 0, 0
	seen := map[string]bool{}
	for _, node := range snapshot.Nodes {
		id := strings.ToLower(node.Status.NodeInfo.SystemUUID)
		unknownIdentity := id == ""
		if id == "" {
			id = string(node.UID)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate Node registration for one instance")
		}
		seen[id] = true
		owned := reserved[id] || node.UID == target.UID
		if owned || unknownIdentity || snapshot.Faulted[node.Name] {
			excluded[node.Name] = true
		}
		if owned || unknownIdentity || snapshot.Faulted[node.Name] || !ready(node) || node.Spec.Unschedulable {
			unavailable[id] = location{poolName(node), node.Labels[corev1.LabelTopologyZone]}
		} else {
			if poolName(node) == poolName(target) {
				healthyPool++
			}
			if node.Labels[corev1.LabelTopologyZone] == target.Labels[corev1.LabelTopologyZone] {
				healthyZone++
			}
		}
	}
	poolUnavailable, zoneUnavailable := 0, 0
	for _, location := range unavailable {
		if location.pool == poolName(target) {
			poolUnavailable++
		}
		if location.zone == target.Labels[corev1.LabelTopologyZone] {
			zoneUnavailable++
		}
	}
	if len(unavailable) > cfg.MaxUnavailableCluster || poolUnavailable > cfg.MaxUnavailablePool || zoneUnavailable > cfg.MaxUnavailableZone {
		return nil, fmt.Errorf("cluster, pool or zone unavailable limit reached")
	}
	if healthyPool < cfg.MinHealthyPool || healthyZone < cfg.MinHealthyZone {
		return nil, fmt.Errorf("healthy capacity floor would be breached")
	}
	if baseline < 10 && healthyPool < int(baseline) {
		return nil, fmt.Errorf("small-pool deletion requires ready replacement capacity")
	}
	return excluded, nil
}

func (c *Controller) reserveDeletion(ctx context.Context, revision uint64, reservation api.MitigationReservation, budget *api.NodeMitigationBudget) error {
	for _, existing := range budget.Status.Reservations {
		if existing.NodeUID == reservation.NodeUID || existing.InstanceID == reservation.InstanceID {
			return fmt.Errorf("node or instance already has an active execution owner")
		}
	}
	if len(budget.Status.Reservations) >= maxRecords {
		return fmt.Errorf("reservation storage limit reached")
	}
	if budget.Status.Reservations == nil {
		budget.Status.Reservations = map[string]api.MitigationReservation{}
	}
	reservation.ReservedAt = metav1.NewTime(c.clock())
	budget.Status.Reservations[recordName(string(reservation.NodeUID))] = reservation
	return c.saveBudget(ctx, revision, budget)
}
