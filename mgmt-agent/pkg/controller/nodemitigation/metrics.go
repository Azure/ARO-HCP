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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

var (
	registerMetrics              sync.Once
	instanceVerificationFailures = metrics.NewCounter(&metrics.CounterOpts{
		Name:           "mgmt_agent_node_mitigation_instance_verification_failures_total",
		Help:           "Failed attempts to verify an original Azure instance.",
		StabilityLevel: metrics.ALPHA,
	})
	instanceCleanupStalled = metrics.NewCounter(&metrics.CounterOpts{
		Name:           "mgmt_agent_node_mitigation_instance_cleanup_stalled_observations_total",
		Help:           "Observations of an original instance remaining after the cleanup warning threshold.",
		StabilityLevel: metrics.ALPHA,
	})
	actionResults = metrics.NewCounterVec(&metrics.CounterOpts{
		Name:           "mgmt_agent_node_mitigation_actions_total",
		Help:           "Submitted Kubernetes mitigation actions by operation and API outcome.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"action", "outcome"})
	activeConditions = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name:           "mgmt_agent_node_mitigation_active_conditions",
		Help:           "Active mitigation episodes reporting each warning or safety condition.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"condition"})
	budgetUsage = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name:           "mgmt_agent_node_mitigation_budget",
		Help:           "Pool deletion limit, active reservations and available rolling allowance.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"pool", "kind"})
	recoveryObservations = metrics.NewCounterVec(&metrics.CounterOpts{
		Name:           "mgmt_agent_node_mitigation_workload_recovery_observations_total",
		Help:           "Workload recovery observations by readiness outcome.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"outcome"})
	recoveryWait = metrics.NewGauge(&metrics.GaugeOpts{
		Name:           "mgmt_agent_node_mitigation_workload_recovery_wait_seconds",
		Help:           "Longest outstanding workload recovery wait in the management cluster.",
		StabilityLevel: metrics.ALPHA,
	})
)

func RegisterMetrics() {
	registerMetrics.Do(func() {
		legacyregistry.MustRegister(instanceVerificationFailures, instanceCleanupStalled,
			actionResults, activeConditions, budgetUsage, recoveryObservations, recoveryWait)
	})
}

func (c *Controller) action(revision uint64, kind string, fn func() error) error {
	err := c.write(revision, fn)
	if errors.Is(err, ErrPaused) {
		return err
	}
	outcome := "succeeded"
	switch {
	case apierrors.IsConflict(err):
		outcome = "conflict"
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		outcome = "denied"
	case apierrors.IsTooManyRequests(err):
		outcome = "throttled"
	case err != nil:
		outcome = "failed"
	}
	actionResults.WithLabelValues(kind, outcome).Inc()
	return err
}

func reportState(episodes []api.MitigationEpisode, budget *api.NodeMitigationBudget, now time.Time) {
	longest := float64(0)
	for _, episode := range episodes {
		if recovery := episode.Status.Recovery; recovery != nil && !recovery.StartedAt.IsZero() {
			longest = max(longest, now.Sub(recovery.StartedAt.Time).Seconds())
		}
	}
	recoveryWait.Set(longest)
	for _, condition := range []string{"Held", "InstanceCleanupStalled", "InstanceVerificationUnavailable"} {
		count := 0
		for _, episode := range episodes {
			if episode.Status.Phase != PhaseComplete && meta.IsStatusConditionTrue(episode.Status.Conditions, condition) {
				count++
			}
		}
		activeConditions.WithLabelValues(condition).Set(float64(count))
	}
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
