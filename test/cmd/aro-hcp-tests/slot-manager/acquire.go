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
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func DefaultAcquireOptions() *RawAcquireOptions {
	return &RawAcquireOptions{
		ClusterProfileDir:   os.Getenv("CLUSTER_PROFILE_DIR"),
		DeployEnv:           os.Getenv("ARO_HCP_DEPLOY_ENV"),
		Region:              defaultAcquireRegion(),
		SharedDir:           os.Getenv("SHARED_DIR"),
		LeaseProxyServerURL: os.Getenv("LEASE_PROXY_SERVER_URL"),
		LeaseProxyTimeout:   slots.DefaultLeaseProxyTimeout,
	}
}

func defaultAcquireRegion() string {
	for _, value := range []string{
		os.Getenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION"),
		os.Getenv("LOCATION"),
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func BindAcquireOptions(opts *RawAcquireOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ClusterProfileDir, "cluster-profile-dir", opts.ClusterProfileDir, "Path to CLUSTER_PROFILE_DIR")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "Deploy environment name (prow, ci01, int, stg, prod)")
	cmd.Flags().StringVar(&opts.SubscriptionName, "subscription-name", opts.SubscriptionName, "Optional subscription name selector for environments with multiple pools.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Optional runtime region selector. For fixed pools it also participates in pool selection.")
	cmd.Flags().StringVar(&opts.SharedDir, "shared-dir", opts.SharedDir, "Path to SHARED_DIR")
	cmd.Flags().StringVar(&opts.CatalogPath, "slot-catalog", opts.CatalogPath, "Path to the canonical E2E slot catalog")
	cmd.Flags().StringVar(&opts.LeaseProxyServerURL, "lease-proxy-server-url", opts.LeaseProxyServerURL, "Lease proxy server URL")
	cmd.Flags().DurationVar(&opts.LeaseProxyTimeout, "lease-proxy-timeout", opts.LeaseProxyTimeout, "Timeout per lease proxy request attempt")
	return nil
}

type RawAcquireOptions struct {
	ClusterProfileDir   string
	DeployEnv           string
	SubscriptionName    string
	Region              string
	SharedDir           string
	CatalogPath         string
	LeaseProxyServerURL string
	LeaseProxyTimeout   time.Duration
}

type validatedAcquireOptions struct {
	*RawAcquireOptions
}

type ValidatedAcquireOptions struct {
	*validatedAcquireOptions
}

type completedAcquireOptions struct {
	DeployEnvironment    string
	SharedDir            string
	LeaseProxyURL        string
	LeaseProxyTimeout    time.Duration
	RuntimeRegion        string
	CustomerSubscription string
	Pool                 slots.Pool
	PoolEnvironment      string
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
	case strings.TrimSpace(o.ClusterProfileDir) == "":
		return nil, fmt.Errorf("--cluster-profile-dir must not be empty")
	case strings.TrimSpace(o.DeployEnv) == "":
		return nil, fmt.Errorf("--deploy-env must not be empty")
	case strings.TrimSpace(o.SharedDir) == "":
		return nil, fmt.Errorf("--shared-dir must not be empty")
	case strings.TrimSpace(o.LeaseProxyServerURL) == "":
		return nil, fmt.Errorf("--lease-proxy-server-url must not be empty")
	case o.LeaseProxyTimeout <= 0:
		return nil, fmt.Errorf("--lease-proxy-timeout must be greater than zero")
	}

	return &ValidatedAcquireOptions{
		validatedAcquireOptions: &validatedAcquireOptions{RawAcquireOptions: o},
	}, nil
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

	pool, err := catalog.ResolvePool(environment, o.SubscriptionName, o.Region)
	if err != nil {
		return nil, err
	}

	runtimeRegion := strings.TrimSpace(o.Region)
	if runtimeRegion == "" {
		runtimeRegion = pool.Region
	}

	customerSubscription, err := slots.ResolveCustomerSubscriptionName(o.ClusterProfileDir, pool.SubscriptionName)
	if err != nil {
		return nil, err
	}

	return &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			DeployEnvironment:    o.DeployEnv,
			SharedDir:            o.SharedDir,
			LeaseProxyURL:        o.LeaseProxyServerURL,
			LeaseProxyTimeout:    o.LeaseProxyTimeout,
			RuntimeRegion:        runtimeRegion,
			CustomerSubscription: customerSubscription,
			Pool:                 pool,
			PoolEnvironment:      environment,
		},
	}, nil
}

func (o *AcquireOptions) ResolveLeasedSlot(resourceName string) (*slots.ExpandedSlot, error) {
	for _, slot := range slots.ExpandSlotsForPool(o.PoolEnvironment, o.Pool) {
		if slot.ResourceName == resourceName {
			resolvedSlot := slot
			return &resolvedSlot, nil
		}
	}

	return nil, fmt.Errorf(
		"leased resource %q is not part of selected pool (subscription_name=%q, region=%q) in environment %q",
		resourceName,
		o.Pool.SubscriptionName,
		o.Pool.Region,
		o.PoolEnvironment,
	)
}

func (o *AcquireOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	leasedName, err := slots.AcquireLease(ctx, o.LeaseProxyURL, o.Pool.ResourceType, o.LeaseProxyTimeout)
	if err != nil {
		return err
	}

	releaseOnError := true
	defer func() {
		if releaseOnError {
			if err := slots.ReleaseLease(ctx, o.LeaseProxyURL, leasedName, o.LeaseProxyTimeout); err != nil {
				logger.Error(err, "Failed to release lease after acquire error", "name", leasedName)
			}
		}
	}()

	slot, err := o.ResolveLeasedSlot(leasedName)
	if err != nil {
		return err
	}

	state := &slots.AcquiredSlotState{
		Version:            1,
		DeployEnvironment:  o.DeployEnvironment,
		RuntimeRegion:      o.RuntimeRegion,
		Slot:               *slot,
		LeasedResourceName: leasedName,
	}
	if err := slots.WriteAcquiredSlotState(o.SharedDir, state); err != nil {
		return err
	}
	if err := slots.WriteEnvFile(o.SharedDir, state, o.CustomerSubscription); err != nil {
		return err
	}

	releaseOnError = false
	logger.Info("Acquired slot and wrote shared artifacts", "slotName", slot.ResourceName, "environment", o.PoolEnvironment, "sharedDir", o.SharedDir)
	return nil
}
