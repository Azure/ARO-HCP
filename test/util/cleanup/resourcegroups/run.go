// Copyright 2025 Microsoft Corporation
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

package resourcegroups

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func (o *Options) Run(ctx context.Context) error {
	tc := framework.NewTestContext()
	resourceGroupsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()

	resourceGroupsToDelete, err := o.discoverResourceGroups(ctx, resourceGroupsClient)
	if err != nil {
		return err
	}
	return o.cleanupResourceGroups(ctx, tc, resourceGroupsToDelete)
}

func (o *Options) discoverResourceGroups(ctx context.Context, resourceGroupsClient *armresources.ResourceGroupsClient) ([]string, error) {
	logger := logr.FromContextOrDiscard(ctx)
	var resourceGroupsToDelete []string

	// If resource groups are explicitly provided, filter to existing ones
	if len(o.ResourceGroups) > 0 {
		existingResourceGroups := sets.New[string]()
		resourceGroupLocations := map[string]string{}
		resourceGroupsPager := resourceGroupsClient.NewListPager(nil)
		for resourceGroupsPager.More() {
			page, err := resourceGroupsPager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed listing resource groups: %w", err)
			}
			for _, rg := range page.Value {
				existingResourceGroups.Insert(strings.ToLower(*rg.Name))
				resourceGroupLocations[strings.ToLower(*rg.Name)] = *rg.Location
			}
		}

		requestedResourceGroups := sets.New[string]()
		for _, name := range o.ResourceGroups {
			requestedResourceGroups.Insert(strings.ToLower(name))
		}
		resourceGroupsToDelete = requestedResourceGroups.Intersection(existingResourceGroups).UnsortedList()
		resourceGroupsNotFound := requestedResourceGroups.Difference(existingResourceGroups).UnsortedList()

		for _, rg := range resourceGroupsNotFound {
			logger.Info("Resource group does not exist, skipping", "name", rg)
		}

		resourceGroupsToDelete = filterResourceGroupsByLocation(resourceGroupsToDelete, resourceGroupLocations,
			o.IncludeLocations, o.ExcludeLocations, logger)

	} else if o.DeleteExpired {

		expiredResourceGroups, err := framework.ListAllExpiredResourceGroups(ctx, resourceGroupsClient, o.EvaluationTime)
		if err != nil {
			return nil, fmt.Errorf("failed to list expired resource groups: %w", err)
		}

		resourceGroupsToDelete = make([]string, 0, len(expiredResourceGroups))
		resourceGroupLocations := map[string]string{}
		for _, resourceGroup := range expiredResourceGroups {

			location := *resourceGroup.Location
			resourceGroupLocations[*resourceGroup.Name] = location
			resourceGroupsToDelete = append(resourceGroupsToDelete, *resourceGroup.Name)
		}

		resourceGroupsToDelete = filterResourceGroupsByLocation(resourceGroupsToDelete, resourceGroupLocations,
			o.IncludeLocations, o.ExcludeLocations, logger)
	}

	if o.JobID != "" {
		jobResourceGroups, err := framework.ListResourceGroupsByJobID(ctx, resourceGroupsClient, o.JobID)
		if err != nil {
			return nil, err
		}
		var names []string
		locations := map[string]string{}
		for _, resourceGroup := range jobResourceGroups {
			if resourceGroup.Name == nil || resourceGroup.Location == nil {
				return nil, fmt.Errorf("resource group discovered by job ID is missing name or location")
			}
			// A managed resource group, such as an HCP cluster's, is deleted through its
			// owner. Deleting it directly would remove resources from a live cluster.
			if resourceGroup.ManagedBy != nil && *resourceGroup.ManagedBy != "" {
				logger.Info("Skipping managed resource group; it is deleted with its owner", "name", *resourceGroup.Name, "managedBy", *resourceGroup.ManagedBy)
				continue
			}
			names = append(names, *resourceGroup.Name)
			locations[*resourceGroup.Name] = *resourceGroup.Location
		}
		resourceGroupsToDelete = append(resourceGroupsToDelete, filterResourceGroupsByLocation(names, locations,
			o.IncludeLocations, o.ExcludeLocations, logger)...)
	}
	unique := sets.New[string]()
	for _, name := range resourceGroupsToDelete {
		unique.Insert(strings.ToLower(name))
	}
	return sets.List(unique), nil
}

func (o *Options) cleanupResourceGroups(ctx context.Context, tc interface {
	CleanupResourceGroups(context.Context, framework.CleanupResourceGroupsOptions) error
}, resourceGroupsToDelete []string) error {
	logger := logr.FromContextOrDiscard(ctx)
	if len(resourceGroupsToDelete) == 0 {
		logger.Info("No resource groups provided")
		return nil
	}

	if o.DryRun {
		for _, rg := range resourceGroupsToDelete {
			fmt.Println(rg)
		}
		return nil
	}

	logger.Info("Starting resource group deletion", "count", len(resourceGroupsToDelete), "mode", o.CleanupWorkflow,
		"timeout", o.Timeout, "concurrency", o.Concurrency, "include-locations", o.IncludeLocations, "exclude-locations", o.ExcludeLocations,
		"is-development", o.IsDevelopment)

	opts := framework.CleanupResourceGroupsOptions{
		ResourceGroupNames: resourceGroupsToDelete,
		Timeout:            o.Timeout,
		CleanupWorkflow:    o.CleanupWorkflow,
		Concurrency:        o.Concurrency,
		FPACredentials: framework.FPACredentials{
			ClientID: o.FPAClientID,
			CertPath: o.FPACertPath,
		},
	}

	err := tc.CleanupResourceGroups(
		ctx,
		opts)
	if err != nil {
		logger.Error(err, "Failed to delete some resource groups", "count", len(resourceGroupsToDelete))
		return err
	}

	logger.Info("All resource groups successfully deleted", "count", len(resourceGroupsToDelete))
	return nil
}

// filterResourceGroupsByLocation filters a list of resource group names according to include/exclude
// location sets, and logs any skipped resource groups.
func filterResourceGroupsByLocation(
	resourceGroups []string,
	resourceGroupLocations map[string]string,
	includeLocations, excludeLocations sets.Set[string],
	logger logr.Logger,
) []string {
	if includeLocations.Len() == 0 && excludeLocations.Len() == 0 {
		return resourceGroups
	}

	filtered := make([]string, 0, len(resourceGroups))
	for _, rg := range resourceGroups {
		location := resourceGroupLocations[rg]

		if includeLocations.Len() > 0 {
			if !includeLocations.Has(location) {
				logger.V(1).Info("Skipping resource group due to include-location filter", "name", rg, "location", location)
				continue
			}
		} else if excludeLocations.Len() > 0 {
			if excludeLocations.Has(location) {
				logger.V(1).Info("Skipping resource group due to exclude-location filter", "name", rg, "location", location)
				continue
			}
		}

		filtered = append(filtered, rg)
	}

	return filtered
}
