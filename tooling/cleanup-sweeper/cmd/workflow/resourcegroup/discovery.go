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
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
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
	finalCandidates := promoteAndSortDeletionTargets(logger, deletionTargets, allResourceGroups, excluded, opts.Policy.Discovery, opts.ReferenceTime, candidateSources)

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
	hasRules := len(opts.Policy.Discovery.Rules) > 0

	if hasRules && opts.ReferenceTime.IsZero() {
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

	if !hasRules {
		return discoveredResourceGroups, resourceGroups, nil
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

type managedChild struct {
	rg          *armresources.ResourceGroup
	name        string
	parent      string
	parentLower string
}

// promoteAndSortDeletionTargets ensures that for each deletion target, all managed children are also targets (promoting if necessary) and that children are deleted first.
// Deleting a parent also deletes the resource that manages each child, so a target with a protected managed child is dropped together with its children.
func promoteAndSortDeletionTargets(
	logger logr.Logger,
	deletionTargets sets.Set[string],
	allResourceGroups []*armresources.ResourceGroup,
	excludedResourceGroups sets.Set[string],
	discoveryPolicy policy.RGDiscoveryPolicy,
	referenceTime time.Time,
	candidateSources map[string]string,
) []string {
	children := listManagedChildren(logger, allResourceGroups)
	dropTargetsWithProtectedChildren(logger, deletionTargets, children, allResourceGroups, excludedResourceGroups, discoveryPolicy, referenceTime)

	imminentOrphans := sets.New[string]()
	deletionTargetsLower := sets.New[string]()
	for t := range deletionTargets {
		deletionTargetsLower.Insert(strings.ToLower(t))
	}

	for _, child := range children {
		if !deletionTargetsLower.Has(child.parentLower) {
			continue
		}
		nameLower := strings.ToLower(child.name)
		imminentOrphans.Insert(child.name)
		if deletionTargetsLower.Has(nameLower) {
			continue
		}
		deletionTargets.Insert(child.name)
		deletionTargetsLower.Insert(nameLower)
		candidateSources[child.name] = fmt.Sprintf("managed child of deletion target %q", child.parent)
		logger.Info("Adding managed RG to deletion targets (parent scheduled for deletion)",
			"resourceGroup", child.name,
			"parentResourceGroup", child.parent,
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

func listManagedChildren(logger logr.Logger, allResourceGroups []*armresources.ResourceGroup) []managedChild {
	children := []managedChild{}
	for _, rg := range allResourceGroups {
		if rg.Name == nil || rg.ManagedBy == nil {
			continue
		}
		parsed, err := azcorearm.ParseResourceID(*rg.ManagedBy)
		if err != nil {
			logger.Info("failed to parse managedBy resource ID, skipping", "resourceGroup", *rg.Name, "managedBy", *rg.ManagedBy, "error", err)
			continue
		}
		children = append(children, managedChild{
			rg:          rg,
			name:        *rg.Name,
			parent:      parsed.ResourceGroupName,
			parentLower: strings.ToLower(parsed.ResourceGroupName),
		})
	}
	return children
}

// dropTargetsWithProtectedChildren removes every deletion target whose managed subtree contains a protected
// resource group, because deleting the target also deletes the resources that manage that subtree. A managed RG is
// protected when it is excluded or when a skip rule selects it once its parent is gone. Evaluating it as orphaned
// keeps a managedByAlive skip rule from masking other protections. Without a reference time the rules cannot be
// evaluated, so every managed RG is treated as protected.
func dropTargetsWithProtectedChildren(
	logger logr.Logger,
	deletionTargets sets.Set[string],
	children []managedChild,
	allResourceGroups []*armresources.ResourceGroup,
	excludedResourceGroups sets.Set[string],
	discoveryPolicy policy.RGDiscoveryPolicy,
	referenceTime time.Time,
) {
	knownResourceGroups := sets.New[string]()
	for _, rg := range allResourceGroups {
		if rg.Name != nil {
			knownResourceGroups.Insert(strings.ToLower(*rg.Name))
		}
	}
	childrenByParent := map[string][]managedChild{}
	parentOf := map[string]string{}
	for _, child := range children {
		childrenByParent[child.parentLower] = append(childrenByParent[child.parentLower], child)
		parentOf[strings.ToLower(child.name)] = child.parentLower
	}

	type blocker struct {
		child  managedChild
		reason string
	}
	blockedBy := map[string]blocker{}
	for _, parentLower := range sets.List(sets.KeySet(childrenByParent)) {
		var orphanedView sets.Set[string]
		for _, child := range childrenByParent[parentLower] {
			reason, protected := "", false
			switch {
			case excludedResourceGroups.Has(strings.ToLower(child.name)):
				reason, protected = "excluded", true
			case len(discoveryPolicy.Rules) == 0:
			case referenceTime.IsZero():
				reason, protected = "missing-reference-time", true
			default:
				if orphanedView == nil {
					orphanedView = knownResourceGroups.Clone()
					orphanedView.Delete(parentLower)
				}
				_, selection := discoveryPolicy.SelectsResourceGroup(child.rg, excludedResourceGroups, orphanedView, referenceTime)
				if selection.Rule != nil && selection.Rule.Action == policy.RGDiscoveryActionSkip {
					reason, protected = selection.String(), true
				}
			}
			if !protected {
				continue
			}
			// Every ancestor of a protected RG would delete it through the managing resource chain.
			for ancestor := parentLower; ; {
				if _, seen := blockedBy[ancestor]; seen {
					break
				}
				blockedBy[ancestor] = blocker{child: child, reason: reason}
				next, ok := parentOf[ancestor]
				if !ok {
					break
				}
				ancestor = next
			}
		}
	}

	for _, target := range sets.List(deletionTargets) {
		b, blocked := blockedBy[strings.ToLower(target)]
		if !blocked {
			continue
		}
		deletionTargets.Delete(target)
		logger.Info("Skipping deletion target to protect a managed RG",
			"resourceGroup", target,
			"managedResourceGroup", b.child.name,
			"parentResourceGroup", b.child.parent,
			"reason", b.reason,
		)
	}
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
