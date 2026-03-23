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

package cleanup

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/go-logr/logr"

	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
)

// resourceGroupDeleter handles ordered deletion of resources in a resource group
type resourceGroupDeleter struct {
	resourceGroupName string
	subscriptionID    string
	credential        azcore.TokenCredential
	wait              bool
	dryRun            bool
	parallelism       int
}

// Note: Resource group deletion is attempted but may fail if resources remain.
// Warnings are logged instead of failing the entire cleanup.
func (d *resourceGroupDeleter) execute(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get logger from context: %w", err)
	}
	// Dry-run header
	if d.dryRun {
		logger.Info("DRY-RUN MODE - No actual deletions will be performed")
	}
	logger.Info("Starting ordered cleanup workflow")

	eng, err := cleanupengine.ResourceGroupOrderedCleanupWorkflow(
		ctx,
		d.resourceGroupName,
		d.subscriptionID,
		d.credential,
		cleanupengine.WorkflowOptions{
			Wait:        d.wait,
			DryRun:      d.dryRun,
			Parallelism: d.parallelism,
		},
	)
	if err != nil {
		return err
	}

	if err := eng.Run(ctx); err != nil {
		return err
	}

	// Final summary - show what resources remain
	resourcesClient, err := armresources.NewClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resources client: %w", err)
	}

	d.logFinalSummary(ctx, resourcesClient)
	return nil
}

// logFinalSummary logs final statistics about the cleanup operation.
// Errors are logged but never propagated — the summary is informational only.
func (d *resourceGroupDeleter) logFinalSummary(ctx context.Context, client *armresources.Client) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return
	}
	if d.dryRun {
		logger.Info("Dry-run workflow complete; collecting final state")
	} else {
		logger.Info("Ordered cleanup workflow complete; collecting final state")
	}

	// Check what's left in the resource group
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, nil)
	remainingResources := []*armresources.GenericResourceExpanded{}

	// Count remaining resources
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If resource group doesn't exist (was deleted), that's success
			if strings.Contains(err.Error(), "ResourceGroupNotFound") {
				logger.Info("All resources have been deleted from the resource group")
				return
			}
			logger.Info(fmt.Sprintf("[WARNING] Could not verify remaining resources: %v", err))
			return
		}
		remainingResources = append(remainingResources, page.Value...)
	}

	remainingCount := len(remainingResources)

	if remainingCount == 0 {
		logger.Info("All resources have been deleted from the resource group")
		return
	}

	// Group remaining resources by type for detailed reporting
	resourcesByType := make(map[string][]string)
	for _, res := range remainingResources {
		if res.Type != nil && res.Name != nil {
			resType := *res.Type
			resName := *res.Name
			resourcesByType[resType] = append(resourcesByType[resType], resName)
		}
	}

	// Log summary
	if d.dryRun {
		logger.Info(fmt.Sprintf("Resource group cleanup preview completed. %d resources would be deleted", remainingCount))
	} else {
		logger.Info(fmt.Sprintf("[WARNING] Resource group cleanup completed with %d resources remaining", remainingCount))
		if !d.wait {
			logger.Info("[WARNING] Cleanup ran with wait=false, so asynchronous deletes may still be in progress")
		}
		logger.Info("Remaining resources by type:")
		for resType, names := range resourcesByType {
			logger.Info(fmt.Sprintf("  %s: %d resources (%s)", resType, len(names), strings.Join(names, ", ")))
		}
	}
}
