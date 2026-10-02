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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func evictionAllowance(budget capacityreportv1alpha1.NodeMitigationBudgetStatus, cfg Config, owner, node types.UID, now time.Time) error {
	if len(budget.Evictions) >= maxRecords {
		return fmt.Errorf("eviction accounting storage limit reached")
	}
	if err := checkEvictionWindowIncrease(budget, cfg); err != nil {
		return err
	}
	if budget.EvictionWindow.Duration != 0 && budget.EvictionWindow.Duration != cfg.EvictionWindow.Duration && len(budget.Evictions) > 0 {
		return fmt.Errorf("eviction-window decrease requires expired accounting or explicit migration")
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

func checkEvictionWindowIncrease(budget capacityreportv1alpha1.NodeMitigationBudgetStatus, cfg Config) error {
	if budget.EvictionWindow.Duration > 0 && cfg.EvictionWindow.Duration > budget.EvictionWindow.Duration {
		return fmt.Errorf("eviction-window increase requires explicit accounting migration; shorter-window history may already be pruned")
	}
	return nil
}

func (c *Controller) pruneBudget(ctx context.Context, cfg Config, revision uint64, budget *capacityreportv1alpha1.NodeMitigationBudget) error {
	if cfg.Mode == Disabled {
		return nil
	}
	if err := checkEvictionWindowIncrease(budget.Status, cfg); err != nil {
		return err
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

func (c *Controller) saveBudget(ctx context.Context, revision uint64, budget *capacityreportv1alpha1.NodeMitigationBudget) error {
	return c.write(revision, func() error {
		return c.saveBudgetLocked(ctx, budget)
	})
}

// The caller must hold the configuration fence through the status write.
func (c *Controller) saveBudgetLocked(ctx context.Context, budget *capacityreportv1alpha1.NodeMitigationBudget) error {
	result, err := c.records.MgmtagentV1alpha1().NodeMitigationBudgets(c.namespace).UpdateStatus(ctx, budget, metav1.UpdateOptions{})
	if err == nil {
		*budget = *result
	}
	return err
}

func (c *Controller) budget(ctx context.Context, revision uint64, mode Mode) (*capacityreportv1alpha1.NodeMitigationBudget, error) {
	budget, err := c.records.MgmtagentV1alpha1().NodeMitigationBudgets(c.namespace).Get(ctx, budgetName, metav1.GetOptions{})
	missing := apierrors.IsNotFound(err)
	if err != nil && !missing {
		return nil, err
	}
	if missing {
		budget = &capacityreportv1alpha1.NodeMitigationBudget{ObjectMeta: metav1.ObjectMeta{Name: budgetName, Namespace: c.namespace}}
	}
	if budget.Status.Version != 1 && (budget.Status.Version != 0 || len(budget.Status.Evictions) > 0) {
		return nil, fmt.Errorf("unsupported mitigation accounting version %d; explicit migration required", budget.Status.Version)
	}
	if len(budget.Status.Evictions) > 0 && budget.Status.EvictionWindow.Duration <= 0 {
		return nil, fmt.Errorf("nonempty mitigation accounting requires a positive eviction window; explicit migration required")
	}
	for _, owner := range budget.OwnerReferences {
		if owner.Kind == "Node" {
			return nil, fmt.Errorf("mitigation accounting must not have a Node owner reference; operator reconciliation required")
		}
	}
	now := c.clock()
	for id, record := range budget.Status.Evictions {
		if id == "" || record.WorkloadUID == "" || record.NodeUID == "" || record.PodUID == "" {
			return nil, fmt.Errorf("mitigation accounting record %q has missing identities; operator reconciliation required", id)
		}
		if record.AttemptedAt.IsZero() || record.AttemptedAt.After(now) {
			return nil, fmt.Errorf("mitigation accounting record %q has an invalid attempt timestamp; operator reconciliation required", id)
		}
	}
	if budget.Status.Version == 0 && mode != Disabled {
		if err := c.checkAccountingOwnership(ctx); err != nil {
			return nil, err
		}
	}
	if missing && mode == Enforce {
		if err := c.write(revision, func() error {
			var createErr error
			budget, createErr = c.records.MgmtagentV1alpha1().NodeMitigationBudgets(c.namespace).Create(ctx, budget, metav1.CreateOptions{})
			return createErr
		}); err != nil {
			return nil, err
		}
	}
	initialize := budget.Status.Version == 0
	budget.Status.Version = 1
	if budget.Status.Evictions == nil {
		budget.Status.Evictions = map[string]capacityreportv1alpha1.EvictionRecord{}
	}
	if initialize && mode == Enforce {
		return budget, c.saveBudget(ctx, revision, budget)
	}
	return budget, nil
}

func (c *Controller) checkAccountingOwnership(ctx context.Context) error {
	options := metav1.ListOptions{LabelSelector: ownershipLabel + "=" + ControllerName, Limit: 1}
	pods, err := c.kube.CoreV1().Pods("").List(ctx, options)
	if err != nil {
		return fmt.Errorf("check pod ownership before initializing accounting: %w", err)
	}
	if len(pods.Items) > 0 {
		return fmt.Errorf("owned pod exists without initialized accounting; operator reconciliation required")
	}
	return nil
}
