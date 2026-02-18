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
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	tc := framework.NewTestContext()
	resourceGroupsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()

	var resourceGroupsToDelete []string

	// If resource groups are explicitly provided, filter to existing ones
	if len(o.ResourceGroups) > 0 {
		existingResourceGroups := sets.New[string]()
		resourceGroupLocations := map[string]string{}
		resourceGroupsPager := resourceGroupsClient.NewListPager(nil)
		for resourceGroupsPager.More() {
			page, err := resourceGroupsPager.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("failed listing resource groups: %w", err)
			}
			for _, rg := range page.Value {
				existingResourceGroups.Insert(*rg.Name)
				resourceGroupLocations[*rg.Name] = *rg.Location
			}
		}

		requestedResourceGroups := sets.New(o.ResourceGroups...)
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
			return fmt.Errorf("failed to list expired resource groups: %w", err)
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
		"timeout", o.Timeout, "include-locations", o.IncludeLocations, "exclude-locations", o.ExcludeLocations,
		"is-development", o.IsDevelopment)

	opts := framework.CleanupResourceGroupsOptions{
		ResourceGroupNames: resourceGroupsToDelete,
		Timeout:            o.Timeout,
		CleanupWorkflow:    o.CleanupWorkflow,
	}

	err := tc.CleanupResourceGroups(
		ctx,
		opts)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.RawResponse != nil {
			// Extract the Correlation ID header
			corrID := respErr.RawResponse.Header.Get("x-ms-correlation-request-id")

			logger.Error(err, "Failed to delete some resource groups",
				"count", len(resourceGroupsToDelete),
				"correlationID", corrID)

			return fmt.Errorf("cleanup failed (CorrelationID: %s): %w", corrID, err)
		}

		// Fallback for non-Azure errors
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
