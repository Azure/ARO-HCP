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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

type PoolObservation struct {
	ID         string
	Target     int32
	Stable     bool
	Ready      int
	ObservedAt time.Time
}

func updateBaseline(previous *api.PoolBaseline, observed PoolObservation, cfg Config, observer string, now time.Time) (api.PoolBaseline, error) {
	if observed.ID == "" || observed.Target < 0 || observed.ObservedAt.After(now) ||
		now.Sub(observed.ObservedAt) > cfg.ObservationMaxAge.Duration {
		return api.PoolBaseline{}, fmt.Errorf("pool observation unavailable or stale")
	}
	if previous == nil {
		if !observed.Stable {
			return api.PoolBaseline{}, fmt.Errorf("pool operation in progress")
		}
		return api.PoolBaseline{Size: observed.Target, Target: observed.Target, ObservedAt: metav1.NewTime(now), Observer: observer}, nil
	}
	next := *previous
	if observed.Target < next.Size {
		next.Size = observed.Target
	}
	continuous := previous.Observer == observer &&
		!previous.ObservedAt.After(now) && now.Sub(previous.ObservedAt.Time) <= cfg.ObservationMaxAge.Duration
	if observed.Target > next.Size && observed.Stable {
		if !continuous || previous.Target != observed.Target || next.StableSince == nil {
			t := metav1.NewTime(now)
			next.StableSince = &t
		}
		if now.Sub(next.StableSince.Time) >= cfg.Window.Duration && observed.Ready >= int(observed.Target) {
			next.Size = observed.Target
			next.StableSince = nil
		}
	} else {
		next.StableSince = nil
	}
	next.Target, next.ObservedAt, next.Observer = observed.Target, metav1.NewTime(now), observer
	return next, nil
}

func deletionLimit(size int32) int {
	if size <= 0 {
		return 0
	}
	if size < 10 {
		return 1
	}
	return int(size / 10)
}

// allowance counts every active reservation, including unknown outcomes, and
// released deletions still inside the window. Retries keep the same reservation.
func allowance(budget api.NodeMitigationBudgetStatus, poolID, ownReservation string, now time.Time, window time.Duration) int {
	used := 0
	for name, reservation := range budget.Reservations {
		if name == ownReservation || reservation.PoolID != poolID {
			continue
		}
		if reservation.ReleasedAt == nil ||
			(reservation.DeleteStartedAt != nil && !reservation.DeleteStartedAt.Before(&metav1.Time{Time: now.Add(-window)})) {
			used++
		}
	}
	return deletionLimit(budget.Pools[poolID].Size) - used
}

func evictionAllowance(budget api.NodeMitigationBudgetStatus, cfg Config, owner, node types.UID, now time.Time) error {
	if len(budget.Evictions) >= maxRecords {
		return fmt.Errorf("eviction accounting storage limit reached")
	}
	if budget.EvictionWindow.Duration != 0 && budget.EvictionWindow.Duration != cfg.EvictionWindow.Duration && len(budget.Evictions) > 0 {
		return fmt.Errorf("eviction-window change requires expired accounting or explicit migration")
	}
	workloadCount, nodeCount := 0, 0
	for _, record := range budget.Evictions {
		age := now.Sub(record.AttemptedAt.Time)
		if age > cfg.EvictionWindow.Duration {
			continue
		}
		if record.WorkloadUID == owner {
			workloadCount++
		}
		if record.NodeUID == node {
			nodeCount++
		}
		if (record.WorkloadUID == owner || record.NodeUID == node) && age < cfg.EvictionCooldown.Duration {
			return fmt.Errorf("eviction cooldown has not elapsed")
		}
	}
	if workloadCount >= cfg.MaxEvictionsPerWorkload || nodeCount >= cfg.MaxEvictionsPerNode {
		return fmt.Errorf("workload or node eviction rate limit reached")
	}
	return nil
}

func (c *Controller) pruneBudget(ctx context.Context, cfg Config, revision uint64, budget *api.NodeMitigationBudget) error {
	if cfg.Mode == Disabled {
		return nil
	}
	changed := false
	for key, record := range budget.Status.Evictions {
		if c.clock().Sub(record.AttemptedAt.Time) > budget.Status.EvictionWindow.Duration {
			delete(budget.Status.Evictions, key)
			changed = true
		}
	}
	for key, reservation := range budget.Status.Reservations {
		if reservation.ReleasedAt == nil || c.clock().Sub(reservation.ReleasedAt.Time) <= budget.Status.Window.Duration {
			continue
		}
		if reservation.DeleteStartedAt != nil && c.clock().Sub(reservation.DeleteStartedAt.Time) <= budget.Status.Window.Duration {
			continue
		}
		delete(budget.Status.Reservations, key)
		delete(c.nextPoll, key)
		changed = true
	}
	if changed && cfg.Mode == Enforce {
		return c.saveBudget(ctx, revision, budget)
	}
	return nil
}
