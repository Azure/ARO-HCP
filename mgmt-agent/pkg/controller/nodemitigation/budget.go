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

	"k8s.io/apimachinery/pkg/types"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

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
	if changed && cfg.Mode == Enforce {
		return c.saveBudget(ctx, revision, budget)
	}
	return nil
}
