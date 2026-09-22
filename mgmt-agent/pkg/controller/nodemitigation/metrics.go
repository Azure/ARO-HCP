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
	"errors"
	"sync"
	"time"

	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

var (
	registerMetrics sync.Once
	actionResults   = metrics.NewCounterVec(&metrics.CounterOpts{
		Name:           "mgmt_agent_node_mitigation_actions_total",
		Help:           "Mitigation API submissions by action and acceptance outcome.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"action", "outcome"})
	operationResults = metrics.NewCounterVec(&metrics.CounterOpts{
		Name:           "mgmt_agent_node_mitigation_operation_observations_total",
		Help:           "AKS deletion operation observations by outcome.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"outcome"})
	budgetUsage = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name:           "mgmt_agent_node_mitigation_budget",
		Help:           "Pool deletion limit, active reservations and available rolling allowance.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"pool", "kind"})
	poolCapacity = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name:           "mgmt_agent_node_mitigation_pool_capacity",
		Help:           "Observed target and healthy capacity for pools with deletion operations.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"pool", "kind"})
	workloadAvailability = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name:           "mgmt_agent_node_mitigation_workload_available_replicas",
		Help:           "Available replicas in the most recently evaluated workload; workload identities are recorded in logs.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"kind"})
)

func RegisterMetrics() {
	registerMetrics.Do(func() {
		legacyregistry.MustRegister(actionResults, operationResults, budgetUsage, poolCapacity, workloadAvailability)
	})
}

func actionOutcome(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, ErrPaused):
		return "paused"
	case apierrors.IsConflict(err):
		return "conflict"
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return "denied"
	case apierrors.IsTooManyRequests(err):
		var status apierrors.APIStatus
		if errors.As(err, &status) && status.Status().Details != nil {
			for _, cause := range status.Status().Details.Causes {
				if cause.Type == policyv1.DisruptionBudgetCause {
					return "pdb_denied"
				}
			}
		}
		return "throttled"
	default:
		return "unknown"
	}
}

func (c *Controller) action(revision uint64, kind string, fn func() error) error {
	err := c.write(revision, fn)
	if !errors.Is(err, ErrPaused) {
		actionResults.WithLabelValues(kind, actionOutcome(err)).Inc()
	}
	return err
}

func reportState(budget *api.NodeMitigationBudget, now time.Time) {
	budgetUsage.Reset()
	for id, baseline := range budget.Status.Pools {
		active := 0
		for _, reservation := range budget.Status.Reservations {
			if reservation.PoolID == id && reservation.ReleasedAt == nil {
				active++
			}
		}
		pool := poolFromID(id)
		budgetUsage.WithLabelValues(pool, "limit").Set(float64(deletionLimit(baseline.Size)))
		budgetUsage.WithLabelValues(pool, "active").Set(float64(active))
		budgetUsage.WithLabelValues(pool, "available").Set(float64(allowance(budget.Status, id, "", now, budget.Status.Window.Duration)))
	}
}
