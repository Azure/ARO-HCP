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

package resourcegroup

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"k8s.io/apimachinery/pkg/util/sets"

	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

func discoverCandidates(ctx context.Context, opts RunOptions) ([]string, error) {
	logger := cleanuprunner.LoggerFromContext(ctx)

	candidateSources := map[string]string{}
	for _, resourceGroup := range sets.List(opts.ResourceGroups) {
		candidateSources[resourceGroup] = "provided via CLI args"
	}

	discoveredCandidates, err := discoverPolicyCandidates(ctx, opts, candidateSources)
	if err != nil {
		return nil, fmt.Errorf("failed to discover resource groups: %w", err)
	}

	finalCandidates := sets.List(opts.ResourceGroups.Union(discoveredCandidates))
	for _, resourceGroup := range finalCandidates {
		source := candidateSources[resourceGroup]
		if strings.TrimSpace(source) == "" {
			source = "unknown source"
		}
		logger.V(1).Info(
			"RG candidate source for rg-ordered workflow",
			"resourceGroup", resourceGroup,
			"source", source,
		)
	}
	if len(finalCandidates) > 0 {
		logger.Info(
			"Final RG candidates for rg-ordered workflow",
			"count", len(finalCandidates),
			"resourceGroups", finalCandidates,
		)
	}

	return finalCandidates, nil
}

func discoverPolicyCandidates(
	ctx context.Context,
	opts RunOptions,
	candidateSources map[string]string,
) (sets.Set[string], error) {
	logger := cleanuprunner.LoggerFromContext(ctx)

	discoveredResourceGroups := sets.New[string]()
	if !opts.DiscoverResourceGroups {
		return discoveredResourceGroups, nil
	}
	if opts.ReferenceTime.IsZero() {
		return nil, fmt.Errorf("reference time is required for resource group discovery")
	}

	rgClient, err := armresources.NewResourceGroupsClient(opts.SubscriptionID, opts.AzureCredential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource groups client: %w", err)
	}

	resourceGroups, err := listResourceGroups(ctx, rgClient)
	if err != nil {
		if opts.FailOnDiscoveryError {
			return nil, fmt.Errorf("failed listing resource groups: %w", err)
		}
		logger.Info("Failed to list resource groups; continuing with explicit targets", "error", err)
		return discoveredResourceGroups, nil
	}

	excludedResourceGroups := sets.New(opts.Policy.ExcludedResourceGroups...)

	for _, rg := range resourceGroups {
		include, reason := opts.Policy.Discovery.SelectsResourceGroup(rg, excludedResourceGroups, opts.ReferenceTime)
		if !include {
			continue
		}
		discoveredResourceGroups.Insert(*rg.Name)
		if _, provided := candidateSources[*rg.Name]; !provided {
			candidateSources[*rg.Name] = reason.SourceDescription()
		}
		logger.V(1).Info("Discovered RG candidate from policy", "resourceGroup", *rg.Name, "reason", reason.String())
	}

	return discoveredResourceGroups, nil
}
func listResourceGroups(
	ctx context.Context,
	rgClient *armresources.ResourceGroupsClient,
) ([]*armresources.ResourceGroup, error) {
	pager := rgClient.NewListPager(nil)
	resourceGroups := []*armresources.ResourceGroup{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		resourceGroups = append(resourceGroups, page.Value...)
	}
	return resourceGroups, nil
}
