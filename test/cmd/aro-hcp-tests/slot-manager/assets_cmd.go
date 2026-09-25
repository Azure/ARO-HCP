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
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	identitypool "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/identity-pool"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type assetCommandOptions struct {
	Environment   string
	SlotCatalog   string
	Subscriptions []string
	Pools         []string
	AssetKinds    []string
	Out           io.Writer
}

func newAssetRegistry() (*assets.Registry, error) {
	return assets.NewRegistry(identitypool.NewHandler())
}

func newApplyPoolAssetsCommand(registry *assets.Registry) (*cobra.Command, error) {
	options := &assetCommandOptions{}
	command := &cobra.Command{
		Use:   "apply-pool-assets",
		Short: "Apply every managed asset declared by selected slot pools.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Out = cmd.OutOrStdout()
			return runPoolAssetsCommand(cmd.Context(), registry, options, false)
		},
	}
	if err := bindAssetCommandOptions(command, options); err != nil {
		return nil, err
	}
	return command, nil
}

func newValidatePoolAssetsCommand(registry *assets.Registry) (*cobra.Command, error) {
	options := &assetCommandOptions{}
	command := &cobra.Command{
		Use:   "validate-pool-assets",
		Short: "Validate every backing asset declared by selected slot pools.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Out = cmd.OutOrStdout()
			return runPoolAssetsCommand(cmd.Context(), registry, options, true)
		},
	}
	if err := bindAssetCommandOptions(command, options); err != nil {
		return nil, err
	}
	return command, nil
}

func newIdentityPoolCompatibilityCommand(registry *assets.Registry, validate bool) (*cobra.Command, error) {
	options := &assetCommandOptions{AssetKinds: []string{string(assets.KindE2EIdentities)}}
	use := "apply-identity-pool"
	short := "Apply the managed identity pool through the generic asset registry."
	if validate {
		use = "validate-identity-pool"
		short = "Validate the managed identity pool through the generic asset registry."
	}
	command := &cobra.Command{
		Use:        use,
		Short:      short,
		Deprecated: fmt.Sprintf("use %s-pool-assets --asset %s", map[bool]string{false: "apply", true: "validate"}[validate], assets.KindE2EIdentities),
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Out = cmd.OutOrStdout()
			return runPoolAssetsCommand(cmd.Context(), registry, options, validate)
		},
	}
	command.Flags().StringVar(&options.Environment, "environment", "", "Logical slot environment (dev, int, stg, prod).")
	command.Flags().StringVar(&options.SlotCatalog, "slot-catalog", "", "Path to the canonical E2E slot catalog.")
	command.Flags().StringSliceVar(&options.Subscriptions, "subscription", nil, "Limit operation to E2E subscription name(s). Explicit selection includes unmanaged assets.")
	if err := command.MarkFlagRequired("environment"); err != nil {
		return nil, fmt.Errorf("failed to mark flag %q as required: %w", "environment", err)
	}
	return command, nil
}

func bindAssetCommandOptions(command *cobra.Command, options *assetCommandOptions) error {
	command.Flags().StringVar(&options.Environment, "environment", "", "Logical slot environment (dev, int, stg, prod).")
	command.Flags().StringVar(&options.SlotCatalog, "slot-catalog", "", "Path to the canonical E2E slot catalog.")
	command.Flags().StringSliceVar(&options.Subscriptions, "subscription", nil, "Limit operation to E2E subscription name(s). Explicit selection includes unmanaged assets.")
	command.Flags().StringSliceVar(&options.Pools, "pool", nil, "Limit operation to named v2 pool(s). Explicit selection includes unmanaged assets.")
	command.Flags().StringSliceVar(&options.AssetKinds, "asset", nil, "Limit operation to registered asset kind(s), e.g. e2e_identities.")
	if err := command.MarkFlagRequired("environment"); err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "environment", err)
	}
	return nil
}

func runPoolAssetsCommand(ctx context.Context, registry *assets.Registry, options *assetCommandOptions, validate bool) error {
	if registry == nil {
		return fmt.Errorf("asset registry is nil")
	}
	catalog, err := slots.LoadCatalog(options.SlotCatalog)
	if err != nil {
		return err
	}
	environment, found := catalog.Environments[strings.TrimSpace(options.Environment)]
	if !found {
		return fmt.Errorf("unknown environment %q", options.Environment)
	}

	subscriptionFilter := stringSet(options.Subscriptions)
	poolFilter := stringSet(options.Pools)
	pools := make([]slots.Pool, 0, len(environment.Pools))
	for _, pool := range environment.Pools {
		if len(subscriptionFilter) > 0 {
			if _, found := subscriptionFilter[pool.E2ESubscriptionName()]; !found {
				continue
			}
		}
		if len(poolFilter) > 0 {
			if _, found := poolFilter[pool.Name]; !found {
				continue
			}
		}
		pools = append(pools, pool)
	}
	if len(pools) == 0 {
		return fmt.Errorf("no pools matched environment %q and the requested filters", options.Environment)
	}

	kinds := make([]assets.Kind, 0, len(options.AssetKinds))
	for _, kind := range options.AssetKinds {
		kinds = append(kinds, assets.Kind(kind))
	}
	request := assets.PoolRequest{
		Environment:      options.Environment,
		Pools:            pools,
		IncludeUnmanaged: len(subscriptionFilter) > 0 || len(poolFilter) > 0,
		Out:              options.Out,
	}
	request.Inventories, err = catalog.AssetInventories()
	if err != nil {
		return err
	}
	if validate {
		return registry.ValidatePools(ctx, request, kinds...)
	}
	return registry.ApplyPools(ctx, request, kinds...)
}

func stringSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
