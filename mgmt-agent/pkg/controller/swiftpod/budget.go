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
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/yaml"
)

const (
	BudgetName = "swift-pod-mitigation-budget"
	budgetKey  = "ledger.json"
	maxRecords = 1024
)

type evictionRecord struct {
	WorkloadUID types.UID   `json:"workloadUID"`
	NodeUID     types.UID   `json:"nodeUID"`
	PodUID      types.UID   `json:"podUID"`
	AttemptedAt metav1.Time `json:"attemptedAt"`
}

type ledger struct {
	Version        int                       `json:"version"`
	EvictionWindow metav1.Duration           `json:"evictionWindow"`
	Evictions      map[string]evictionRecord `json:"evictions"`
}

type budget struct {
	ledger
	configMap *corev1.ConfigMap
}

// Reads and validates durable eviction accounting, pruning expired records in memory.
func (c *Controller) readBudget(ctx context.Context, cfg Config) (*budget, error) {
	cm, err := c.kube.CoreV1().ConfigMaps(c.namespace).Get(ctx, BudgetName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read explicitly initialized SWIFT accounting: %w", err)
	}
	if cm.DeletionTimestamp != nil || len(cm.OwnerReferences) != 0 || cm.UID == "" || cm.ResourceVersion == "" {
		return nil, fmt.Errorf("accounting requires persistent identity without owner references or deletion")
	}
	b := &budget{configMap: cm}
	if len(cm.Data[budgetKey]) > 256*1024 {
		return nil, fmt.Errorf("SWIFT accounting exceeds 256 KiB")
	}
	if err := yaml.UnmarshalStrict([]byte(cm.Data[budgetKey]), &b.ledger); err != nil {
		return nil, fmt.Errorf("parse SWIFT accounting: %w", err)
	}
	if b.Version != 1 || b.EvictionWindow.Duration <= 0 || len(b.Evictions) > maxRecords {
		return nil, fmt.Errorf("invalid accounting version, window or record count; explicit reconciliation required")
	}
	if cfg.EvictionWindow.Duration > b.EvictionWindow.Duration {
		return nil, fmt.Errorf("eviction-window increase requires explicit migration; shorter-window history may be pruned")
	}
	now := c.clock()
	for id, record := range b.Evictions {
		if id == "" || record.WorkloadUID == "" || record.NodeUID == "" || record.PodUID == "" ||
			record.AttemptedAt.IsZero() || record.AttemptedAt.After(now) {
			return nil, fmt.Errorf("invalid accounting record %q; explicit reconciliation required", id)
		}
	}
	for id, record := range b.Evictions {
		if now.Sub(record.AttemptedAt.Time) > b.EvictionWindow.Duration {
			delete(b.Evictions, id)
		}
	}
	if cfg.EvictionWindow.Duration != b.EvictionWindow.Duration && len(b.Evictions) > 0 {
		return nil, fmt.Errorf("eviction-window decrease requires expired accounting or explicit migration")
	}
	if b.Evictions == nil {
		b.Evictions = map[string]evictionRecord{}
	}
	return b, nil
}

// Checks accounting capacity, cooldowns and per-workload and per-Node eviction limits.
func evictionAllowance(b *budget, cfg Config, workload, node types.UID, now time.Time) error {
	if len(b.Evictions) >= maxRecords {
		return fmt.Errorf("eviction accounting storage limit reached")
	}
	workloadCount, nodeCount := 0, 0
	for _, record := range b.Evictions {
		age := now.Sub(record.AttemptedAt.Time)
		if age < 0 {
			return fmt.Errorf("accounting timestamp is in the future")
		}
		if age > cfg.EvictionWindow.Duration {
			continue
		}
		if record.WorkloadUID == workload {
			workloadCount++
		}
		if record.NodeUID == node {
			nodeCount++
		}
		if (record.WorkloadUID == workload || record.NodeUID == node) && age < cfg.EvictionCooldown.Duration {
			return fmt.Errorf("eviction cooldown has not elapsed")
		}
	}
	if workloadCount >= cfg.MaxEvictionsPerWorkload || nodeCount >= cfg.MaxEvictionsPerNode {
		return fmt.Errorf("workload or node eviction rate limit reached")
	}
	return nil
}

// Persists accounting with resourceVersion conflict protection.
// The caller holds the configuration fence through recording and submission.
func (c *Controller) saveBudget(ctx context.Context, b *budget) error {
	data, err := json.Marshal(b.ledger)
	if err != nil {
		return err
	}
	if len(data) > 256*1024 {
		return fmt.Errorf("SWIFT accounting exceeds 256 KiB")
	}
	b.configMap.Data[budgetKey] = string(data)
	_, err = c.kube.CoreV1().ConfigMaps(c.namespace).Update(ctx, b.configMap, metav1.UpdateOptions{})
	return err
}
