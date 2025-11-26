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
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func (o Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	tc := framework.NewTestContext()
	resourceGroupsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()

	var resourceGroupsToDelete []string

	// If resource groups are explicitly provided, filter to existing ones
	// deletions occur in 3 tiers (afterSuite -> tracked resource group deletion -> expired resource group deletion)
	// we might receive a resource group that was already deleted
	if len(o.ResourceGroups) > 0 {
		existingResourceGroups := sets.New[string]()
		resourceGroupsPager := resourceGroupsClient.NewListPager(nil)
		for resourceGroupsPager.More() {
			page, err := resourceGroupsPager.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("failed listing resource groups: %w", err)
			}
			for _, rg := range page.Value {
				existingResourceGroups.Insert(*rg.Name)
			}
		}

		requestedResourceGroups := sets.New(o.ResourceGroups...)
		resourceGroupsToDelete = requestedResourceGroups.Intersection(existingResourceGroups).UnsortedList()
		resourceGroupsNotFound := requestedResourceGroups.Difference(existingResourceGroups).UnsortedList()

		for _, rg := range resourceGroupsNotFound {
			logger.Info("Resource group does not exist, skipping", "name", rg)
		}

	} else if o.DeleteExpired {
		// If no resource groups provided, use deleteExpired logic
		// to dynamically list expired resource groups
		now, err := time.Parse(time.RFC3339, o.EvaluationTime)
		if err != nil {
			return fmt.Errorf("failed to parse --evaluation-time value: %w", err)
		}

		expiredResourceGroups, err := framework.ListAllExpiredResourceGroups(
			ctx,
			resourceGroupsClient,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to list expired resource groups: %w", err)
		}

		resourceGroupsToDelete = make([]string, 0, len(expiredResourceGroups))
		for _, resourceGroup := range expiredResourceGroups {
			resourceGroupsToDelete = append(resourceGroupsToDelete, *resourceGroup.Name)
		}
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

	logger.Info("Starting resource group deletion", "count", len(resourceGroupsToDelete))

	err := tc.CleanupResourceGroups(ctx,
		tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		resourceGroupsClient,
		resourceGroupsToDelete)
	if err != nil {
		logger.Error(err, "Failed to delete some resource groups", "count", len(resourceGroupsToDelete))
		return err
	}

	logger.Info("All resource groups successfully deleted", "count", len(resourceGroupsToDelete))
	return nil
}
