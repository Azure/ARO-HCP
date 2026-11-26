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

package acquire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/clusterprofile"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/lease"
)

// RunConfig carries everything Run needs beyond the Plan. Function fields are
// test seams; nil selects the production behavior.
type RunConfig struct {
	AssetInventories       []catalog.AssetInventory
	WriteState             func(string, *lease.AcquiredSlotState) error
	ClusterProfileDirs     []string
	DeployEnvironment      string
	SharedDir              string
	LeaseProxyURL          string
	LeaseProxyTimeout      time.Duration
	MaxWaitForLease        time.Duration
	LeaseWaitInterval      time.Duration
	AssetRegistry          *assets.Registry
	DisabledAssetAdmission []assets.Kind
	AdmissionTimeout       time.Duration
	ResolveSubscriptions   func(context.Context, string, string, string, string) (catalog.ResolvedSubscriptions, error)
	Now                    func() time.Time
	Sleep                  func(context.Context, time.Duration) error
}

// runner binds a Plan to its RunConfig for the duration of one acquisition.
type runner struct {
	*Plan
	RunConfig
}

// Run leases a slot from the first candidate pool that yields one, then
// resolves subscriptions, acquires and admits assets, and publishes the
// runtime contract. A failure after the primary lease is acquired rolls
// everything back.
func (p *Plan) Run(ctx context.Context, config RunConfig) error {
	if config.ResolveSubscriptions == nil {
		config.ResolveSubscriptions = clusterprofile.ResolvePoolSubscriptions
	}
	return (&runner{Plan: p, RunConfig: config}).run(ctx)
}

func (o *runner) run(ctx context.Context) error {
	if o.AssetRegistry == nil {
		return errors.New("asset registry is nil")
	}
	if err := o.AssetRegistry.ValidateRequirements(o.CandidatePools); err != nil {
		return err
	}
	if _, err := o.AssetRegistry.FilterHandlers(o.DisabledAssetAdmission); err != nil {
		return fmt.Errorf("--disable-asset-admission: %w", err)
	}
	stateFile, err := lease.SlotStateFile(o.SharedDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(stateFile); err == nil {
		return errors.New("acquired state already exists; release it before acquiring again")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	envFile, err := lease.EnvFile(o.SharedDir)
	if err != nil {
		return err
	}
	if err := os.Remove(envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	logger := logr.FromContextOrDiscard(ctx)
	now := o.Now
	if now == nil {
		now = time.Now
	}
	candidatePools := rotatedCandidatePools(o.CandidatePools, now())
	sleep := o.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	var deadline time.Time
	if o.MaxWaitForLease > 0 {
		deadline = now().Add(o.MaxWaitForLease)
	}

	for pass := 1; ; pass++ {
		unavailablePools := make([]string, 0, len(candidatePools))

		for _, pool := range candidatePools {
			leasedName, err := lease.AcquireLease(ctx, o.LeaseProxyURL, pool.ResourceType, o.LeaseProxyTimeout)
			if err != nil {
				if errors.Is(err, lease.ErrLeasePoolUnavailableNow) {
					unavailablePools = append(unavailablePools, describePool(pool))
					logger.Info(
						"Candidate pool did not yield an immediate lease, trying next pool",
						"pass", pass,
						"environment", o.PoolEnvironment,
						"pool", describePool(pool),
						"resourceType", pool.ResourceType,
						"error", err.Error(),
					)
					continue
				}

				logger.Error(
					err,
					"Failed to acquire lease from candidate pool",
					"pass", pass,
					"environment", o.PoolEnvironment,
					"pool", describePool(pool),
					"resourceType", pool.ResourceType,
				)
				return err
			}

			return o.finalizeAcquiredLease(ctx, pool, leasedName)
		}

		poolFailureSummary := strings.Join(unavailablePools, ", ")
		waitMessage := "No candidate pool yielded an immediate lease, waiting before retrying full pass"

		if o.MaxWaitForLease > 0 {
			currentTime := now()
			if !currentTime.Before(deadline) {
				return fmt.Errorf(
					"no candidate pool for environment %q yielded an immediate lease for %s across %d full pass(es): %s",
					o.PoolEnvironment,
					o.MaxWaitForLease,
					pass,
					poolFailureSummary,
				)
			}

			remainingWait := deadline.Sub(currentTime)
			sleepDuration := min(o.LeaseWaitInterval, deadline.Sub(currentTime))
			logger.Info(
				waitMessage,
				"pass", pass,
				"environment", o.PoolEnvironment,
				"candidatePoolCount", len(candidatePools),
				"candidatePoolFailures", poolFailureSummary,
				"sleepDuration", sleepDuration,
				"leaseWaitInterval", o.LeaseWaitInterval,
				"remainingWait", remainingWait,
				"maxWaitForLease", o.MaxWaitForLease,
			)
			if err := sleep(ctx, sleepDuration); err != nil {
				return fmt.Errorf("waiting to retry candidate pool pass: %w", err)
			}
			continue
		}

		logger.Info(
			waitMessage,
			"pass", pass,
			"environment", o.PoolEnvironment,
			"candidatePoolCount", len(candidatePools),
			"candidatePoolFailures", poolFailureSummary,
			"sleepDuration", o.LeaseWaitInterval,
			"leaseWaitInterval", o.LeaseWaitInterval,
			"maxWaitForLease", o.MaxWaitForLease,
		)
		if err := sleep(ctx, o.LeaseWaitInterval); err != nil {
			return fmt.Errorf("waiting to retry candidate pool pass: %w", err)
		}
	}
}

func rotatedCandidatePools(pools []catalog.Pool, now time.Time) []catalog.Pool {
	if len(pools) < 2 {
		return pools
	}

	startIndex := int(now.UnixNano() % int64(len(pools)))
	if startIndex == 0 {
		return pools
	}

	rotated := make([]catalog.Pool, 0, len(pools))
	rotated = append(rotated, pools[startIndex:]...)
	rotated = append(rotated, pools[:startIndex]...)
	return rotated
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
