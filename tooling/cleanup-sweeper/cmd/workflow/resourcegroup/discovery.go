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
	"log/slog"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func discoverCandidates(ctx context.Context, opts RunOptions) ([]string, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}

	candidateSources := map[string]string{}
	for _, resourceGroup := range sets.List(opts.ResourceGroups) {
		candidateSources[resourceGroup] = "provided via CLI args"
	}

	discoveredCandidates, allResourceGroups, err := discoverPolicyCandidates(ctx, opts, candidateSources)
	if err != nil {
		return nil, fmt.Errorf("failed to discover resource groups: %w", err)
	}

	deletionTargets := opts.ResourceGroups.Union(discoveredCandidates)
	excluded := sets.New(opts.Policy.ExcludedResourceGroups...)
	finalCandidates := sortDeletionTargets(logger, deletionTargets, allResourceGroups, excluded, candidateSources)

	for _, resourceGroup := range finalCandidates {
		source := candidateSources[resourceGroup]
		if strings.TrimSpace(source) == "" {
			source = "unknown source"
		}
		logger.Info(
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
) (sets.Set[string], []*armresources.ResourceGroup, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}

	discoveredResourceGroups := sets.New[string]()
	if len(opts.Policy.Discovery.Rules) == 0 {
		return discoveredResourceGroups, nil, nil
	}
	if opts.ReferenceTime.IsZero() {
		return nil, nil, fmt.Errorf("reference time is required for resource group discovery")
	}

	rgClient, err := armresources.NewResourceGroupsClient(opts.SubscriptionID, opts.AzureCredential, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource groups client: %w", err)
	}

	resourceGroups, err := listResourceGroups(ctx, rgClient)
	if err != nil {
		logger.Info(
			"Best-effort mode: failed to list resource groups; continuing with explicit targets only",
			"error", err,
		)
		return discoveredResourceGroups, nil, nil
	}

	excludedResourceGroups := sets.New(opts.Policy.ExcludedResourceGroups...)
	knownResourceGroups := make(sets.Set[string], len(resourceGroups))
	for _, rg := range resourceGroups {
		if rg.Name != nil {
			knownResourceGroups.Insert(strings.ToLower(*rg.Name))
		}
	}
	for _, rg := range resourceGroups {
		include, reason := opts.Policy.Discovery.SelectsResourceGroup(rg, excludedResourceGroups, knownResourceGroups, opts.ReferenceTime)
		if !include {
			continue
		}
		discoveredResourceGroups.Insert(*rg.Name)
		if _, provided := candidateSources[*rg.Name]; !provided {
			candidateSources[*rg.Name] = reason.SourceDescription()
		}
		logger.Info("Discovered RG candidate from policy", "resourceGroup", *rg.Name, "reason", reason.String())
	}

	return discoveredResourceGroups, resourceGroups, nil
}

// sortDeletionTargets ensures managed (child) RGs are deleted before their
// parents. If a parent RG is a deletion target, its managed children are
// added to the target set and placed first in the returned list, unblocking
// the parent's VNet/NSG deletion.
func sortDeletionTargets(
	logger logr.Logger,
	deletionTargets sets.Set[string],
	allResourceGroups []*armresources.ResourceGroup,
	excludedResourceGroups sets.Set[string],
	candidateSources map[string]string,
) []string {
	imminentOrphans := sets.New[string]()
	deletionTargetsLower := sets.New[string]()
	for t := range deletionTargets {
		deletionTargetsLower.Insert(strings.ToLower(t))
	}

	for _, rg := range allResourceGroups {
		if rg.Name == nil || rg.ManagedBy == nil {
			continue
		}
		name := *rg.Name
		if excludedResourceGroups.Has(strings.ToLower(name)) {
			continue
		}
		parsed, err := azcorearm.ParseResourceID(*rg.ManagedBy)
		if err != nil {
			slog.Warn("failed to parse managedBy resource ID", "managedBy", *rg.ManagedBy, "error", err)
			continue
		}
		if !deletionTargetsLower.Has(strings.ToLower(parsed.ResourceGroupName)) {
			continue
		}
		imminentOrphans.Insert(name)
		if deletionTargets.Has(name) {
			continue
		}
		deletionTargets.Insert(name)
		candidateSources[name] = fmt.Sprintf("managed child of deletion target %q", parsed.ResourceGroupName)
		logger.Info("Adding managed RG to deletion targets (parent scheduled for deletion)",
			"resourceGroup", name,
			"parentResourceGroup", parsed.ResourceGroupName,
		)
	}

	sorted := make([]string, 0, deletionTargets.Len())
	sorted = append(sorted, sets.List(imminentOrphans)...)
	for _, t := range sets.List(deletionTargets) {
		if !imminentOrphans.Has(t) {
			sorted = append(sorted, t)
		}
	}
	return sorted
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
