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

package slotmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

const (
	DefaultLeaseWaitInterval = 1 * time.Minute
	DefaultMaxWaitForLease   = 30 * time.Minute
)

func DefaultAcquireOptions() *RawAcquireOptions {
	allowedSubscriptions, allowedLocations, selectedLocation := defaultAcquireSelectors()
	return &RawAcquireOptions{
		ClusterProfileDir:    strings.TrimSpace(os.Getenv("CLUSTER_PROFILE_DIR")),
		ClusterProfileDirs:   splitSelectorValues(os.Getenv("CLUSTER_PROFILE_DIRS")),
		DeployEnv:            strings.TrimSpace(os.Getenv("ARO_HCP_DEPLOY_ENV")),
		AllowedSubscriptions: allowedSubscriptions,
		AllowedLocations:     allowedLocations,
		SelectedLocation:     selectedLocation,
		SharedDir:            strings.TrimSpace(os.Getenv("SHARED_DIR")),

		LeaseProxyServerURL: strings.TrimSpace(os.Getenv("LEASE_PROXY_SERVER_URL")),
		LeaseProxyTimeout:   slots.DefaultLeaseProxyTimeout,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	}
}

func defaultAcquireSelectors() ([]string, []string, string) {
	allowedSubscriptions := splitSelectorValues(os.Getenv("ALLOWED_SUBSCRIPTIONS"))
	selectedLocation := strings.TrimSpace(os.Getenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION"))
	if selectedLocation != "" {
		return allowedSubscriptions, nil, selectedLocation
	}

	return allowedSubscriptions, splitSelectorValues(os.Getenv("ALLOWED_LOCATIONS")), ""
}

func splitSelectorValues(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	return normalizeValues(parts)
}

// normalizeValues trims whitespace, drops empty entries, and de-duplicates
// while preserving first-seen order.
func normalizeValues(parts []string) []string {
	values := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func BindAcquireOptions(opts *RawAcquireOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ClusterProfileDir, "cluster-profile-dir", opts.ClusterProfileDir, "Path to CLUSTER_PROFILE_DIR")
	cmd.Flags().StringSliceVar(&opts.ClusterProfileDirs, "cluster-profile-dirs", opts.ClusterProfileDirs, "Optional list of cluster profile dirs to resolve the leased subscription's owning tenant/credentials across. Falls back to --cluster-profile-dir when unset.")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "Deploy environment name (ci00, ci01, int, stg, prod)")
	cmd.Flags().StringSliceVar(&opts.AllowedSubscriptions, "allowed-subscriptions", opts.AllowedSubscriptions, "Optional catalog subscription_name values allowed for candidate pool selection.")
	cmd.Flags().StringSliceVar(&opts.AllowedLocations, "allowed-locations", opts.AllowedLocations, "Optional Azure regions allowed for fixed-mode candidate pool selection.")
	cmd.Flags().StringVar(&opts.SharedDir, "shared-dir", opts.SharedDir, "Path to SHARED_DIR")
	cmd.Flags().StringVar(&opts.CatalogPath, "slot-catalog", opts.CatalogPath, "Path to the canonical E2E slot catalog")
	cmd.Flags().StringVar(&opts.LeaseProxyServerURL, "lease-proxy-server-url", opts.LeaseProxyServerURL, "Lease proxy server URL")
	cmd.Flags().DurationVar(&opts.LeaseProxyTimeout, "lease-proxy-timeout", opts.LeaseProxyTimeout, "Maximum time to spend probing a single candidate pool, including retryable proxy/network retries.")
	cmd.Flags().DurationVar(&opts.MaxWaitForLease, "max-wait-for-lease", opts.MaxWaitForLease, "Maximum total time to keep retrying after full candidate-pool passes yield no immediate lease. Zero waits forever.")
	cmd.Flags().DurationVar(&opts.LeaseWaitInterval, "lease-wait-interval", opts.LeaseWaitInterval, "Wait between retries after a full candidate-pool pass yields no immediate lease.")
	return nil
}

type RawAcquireOptions struct {
	ClusterProfileDir    string
	ClusterProfileDirs   []string
	DeployEnv            string
	AllowedSubscriptions []string
	AllowedLocations     []string
	SelectedLocation     string
	SharedDir            string
	CatalogPath          string
	LeaseProxyServerURL  string
	LeaseProxyTimeout    time.Duration
	MaxWaitForLease      time.Duration
	LeaseWaitInterval    time.Duration
	Now                  func() time.Time
}

type validatedAcquireOptions struct {
	*RawAcquireOptions
}

type ValidatedAcquireOptions struct {
	*validatedAcquireOptions
}

type completedAcquireOptions struct {
	ClusterProfileDirs []string
	DeployEnvironment  string
	SharedDir          string
	LeaseProxyURL      string
	LeaseProxyTimeout  time.Duration
	MaxWaitForLease    time.Duration
	LeaseWaitInterval  time.Duration
	// RuntimeLocationOverride carries the concrete runtime region when pool
	// identity is decoupled from region selection.
	RuntimeLocationOverride string
	CandidatePools          []slots.Pool
	PoolEnvironment         string
	Now                     func() time.Time
	Sleep                   func(context.Context, time.Duration) error
}

type AcquireOptions struct {
	*completedAcquireOptions
}

func newAcquireCommand() (*cobra.Command, error) {
	opts := DefaultAcquireOptions()

	cmd := &cobra.Command{
		Use:   "acquire",
		Short: "Acquire an ARO-HCP E2E slot lease and write shared artifacts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Acquire(cmd.Context(), opts)
		},
	}

	if err := BindAcquireOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

func Acquire(ctx context.Context, opts *RawAcquireOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

func (o *RawAcquireOptions) Validate() (*ValidatedAcquireOptions, error) {
	switch {
	case len(o.effectiveClusterProfileDirs()) == 0:
		return nil, fmt.Errorf("--cluster-profile-dir or --cluster-profile-dirs must not be empty")
	case o.DeployEnv == "":
		return nil, fmt.Errorf("--deploy-env must not be empty")
	case o.SharedDir == "":
		return nil, fmt.Errorf("--shared-dir must not be empty")
	case o.LeaseProxyServerURL == "":
		return nil, fmt.Errorf("--lease-proxy-server-url must not be empty")
	case o.LeaseProxyTimeout <= 0:
		return nil, fmt.Errorf("--lease-proxy-timeout must be greater than zero")
	case o.MaxWaitForLease < 0:
		return nil, fmt.Errorf("--max-wait-for-lease must not be negative")
	case o.LeaseWaitInterval <= 0:
		return nil, fmt.Errorf("--lease-wait-interval must be greater than zero")
	}

	return &ValidatedAcquireOptions{
		validatedAcquireOptions: &validatedAcquireOptions{RawAcquireOptions: o},
	}, nil
}

// effectiveClusterProfileDirs returns the cluster profile dirs to resolve the
// leased subscription's owning credentials across. --cluster-profile-dirs
// (CLUSTER_PROFILE_DIRS) takes precedence; otherwise it falls back to the
// single --cluster-profile-dir (CLUSTER_PROFILE_DIR) for backward
// compatibility. Values are trimmed, de-duplicated, and empty entries dropped
// so callers get a clean list regardless of how they were supplied (flag or
// env, e.g. trailing commas or stray whitespace).
func (o *RawAcquireOptions) effectiveClusterProfileDirs() []string {
	if dirs := normalizeValues(o.ClusterProfileDirs); len(dirs) > 0 {
		return dirs
	}
	if dir := strings.TrimSpace(o.ClusterProfileDir); dir != "" {
		return []string{dir}
	}
	return nil
}

func (o *ValidatedAcquireOptions) Complete(_ context.Context) (*AcquireOptions, error) {
	catalog, err := slots.LoadCatalog(o.CatalogPath)
	if err != nil {
		return nil, err
	}

	environment, err := catalog.ResolveEnvironmentForDeployEnv(o.DeployEnv)
	if err != nil {
		return nil, err
	}

	candidatePools, err := catalog.CandidatePools(environment, sets.New(o.AllowedSubscriptions...), sets.New(o.AllowedLocations...), o.SelectedLocation)
	if err != nil {
		return nil, err
	}

	return &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			ClusterProfileDirs:      o.effectiveClusterProfileDirs(),
			DeployEnvironment:       o.DeployEnv,
			SharedDir:               o.SharedDir,
			LeaseProxyURL:           o.LeaseProxyServerURL,
			LeaseProxyTimeout:       o.LeaseProxyTimeout,
			MaxWaitForLease:         o.MaxWaitForLease,
			LeaseWaitInterval:       o.LeaseWaitInterval,
			RuntimeLocationOverride: o.SelectedLocation,
			CandidatePools:          candidatePools,
			PoolEnvironment:         environment,
			Now:                     o.Now,
			Sleep:                   sleepContext,
		},
	}, nil
}

func (o *AcquireOptions) ResolveLeasedSlot(pool slots.Pool, resourceName string) (*slots.ExpandedSlot, error) {
	for _, slot := range slots.ExpandSlotsForPool(o.PoolEnvironment, pool) {
		if slot.ResourceName == resourceName {
			resolvedSlot := slot
			return &resolvedSlot, nil
		}
	}

	return nil, fmt.Errorf(
		"leased resource %q is not part of selected pool %s in environment %q",
		resourceName,
		describePool(pool),
		o.PoolEnvironment,
	)
}

func (o *AcquireOptions) Run(ctx context.Context) error {
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
			leasedName, err := slots.AcquireLease(ctx, o.LeaseProxyURL, pool.ResourceType, o.LeaseProxyTimeout)
			if err != nil {
				if errors.Is(err, slots.ErrLeasePoolUnavailableNow) {
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

			return o.finalizeAcquiredLease(ctx, logger, pool, leasedName)
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

func rotatedCandidatePools(pools []slots.Pool, now time.Time) []slots.Pool {
	if len(pools) < 2 {
		return pools
	}

	startIndex := int(now.UnixNano() % int64(len(pools)))
	if startIndex == 0 {
		return pools
	}

	rotated := make([]slots.Pool, 0, len(pools))
	rotated = append(rotated, pools[startIndex:]...)
	rotated = append(rotated, pools[:startIndex]...)
	return rotated
}

func (o *AcquireOptions) runtimeRegionForPool(pool slots.Pool) string {
	if o.RuntimeLocationOverride != "" {
		return o.RuntimeLocationOverride
	}
	return pool.Region
}

func (o *AcquireOptions) finalizeAcquiredLease(ctx context.Context, logger logr.Logger, pool slots.Pool, leasedName string) error {
	rollbackLease := true
	defer func() {
		if !rollbackLease {
			return
		}
		if err := slots.ReleaseLease(ctx, o.LeaseProxyURL, leasedName, o.LeaseProxyTimeout); err != nil {
			logger.Error(err, "Failed to release lease after acquire error", "name", leasedName)
		}
	}()

	slot, err := o.ResolveLeasedSlot(pool, leasedName)
	if err != nil {
		return err
	}

	customerSubscription, selectedClusterProfileDir, err := slots.VerifyCustomerSubscriptionName(o.ClusterProfileDirs, pool.SubscriptionName)
	if err != nil {
		return err
	}

	state := &slots.AcquiredSlotState{
		Version:            1,
		DeployEnvironment:  o.DeployEnvironment,
		RuntimeRegion:      o.runtimeRegionForPool(pool),
		Slot:               *slot,
		LeasedResourceName: leasedName,
	}
	// State file must be written before the env file: the release step only
	// needs the state file to return the lease, so if we're killed between
	// the two writes the lease can still be cleaned up. Downstream test
	// steps depend on the env file, but those won't run if acquire didn't
	// complete.
	if err := slots.WriteAcquiredSlotState(o.SharedDir, state); err != nil {
		return err
	}
	// The release step can now clean up this lease from the persisted state
	// file, so subsequent failures should not try to release it again here.
	rollbackLease = false
	if err := slots.WriteEnvFile(o.SharedDir, state, customerSubscription, selectedClusterProfileDir); err != nil {
		return err
	}

	logger.Info(
		"Acquired slot and wrote shared artifacts",
		"slotName", slot.ResourceName,
		"environment", o.PoolEnvironment,
		"pool", describePool(pool),
		"runtimeRegion", state.RuntimeRegion,
		"sharedDir", o.SharedDir,
	)
	return nil
}

func describePool(pool slots.Pool) string {
	if pool.EffectiveRegionMode() == slots.RegionModeRuntimeSelected {
		return fmt.Sprintf("subscription_name=%q, region_mode=%q, default_region=%q", pool.SubscriptionName, pool.EffectiveRegionMode(), pool.Region)
	}
	return fmt.Sprintf("subscription_name=%q, region=%q", pool.SubscriptionName, pool.Region)
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
