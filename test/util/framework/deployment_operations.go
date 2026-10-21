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

package framework

import (
	"context"
	"fmt"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

// fetchOperationsFor retrieves ARM deployment operations, handling both resource-group-scoped
// and subscription-scoped deployments. Recursively fetches child deployment operations,
// resolving the correct subscription client when a nested deployment crosses subscriptions.
func fetchOperationsFor(ctx context.Context, getClient pipeline.OperationsClientGetter, subscriptionID, resourceGroup, deploymentName string) ([]timing.Operation, error) {
	client, err := getClient(subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operations client for subscription: %w", err)
	}

	var operations []timing.Operation

	var deploymentOperations []*armresources.DeploymentOperation
	if resourceGroup != "" {
		// Resource-group scoped deployment
		pager := client.NewListPager(resourceGroup, deploymentName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return []timing.Operation{}, fmt.Errorf("failed to fetch operations: %w", err)
			}
			deploymentOperations = append(deploymentOperations, page.Value...)
		}
	} else {
		// Subscription-scoped deployment
		pager := client.NewListAtSubscriptionScopePager(deploymentName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return []timing.Operation{}, fmt.Errorf("failed to fetch operations: %w", err)
			}
			deploymentOperations = append(deploymentOperations, page.Value...)
		}
	}

	for _, item := range deploymentOperations {
		op, err := operationFor(item)
		if err != nil {
			return []timing.Operation{}, err
		}
		if op != nil {
			operations = append(operations, *op)
		}
	}

	// Recurse into nested deployments, which may target a different subscription
	for i, op := range operations {
		if op.Resource == nil {
			continue
		}
		if strings.EqualFold(op.Resource.ResourceType, "Microsoft.Resources/deployments") {
			// Use the child's subscription if available, otherwise inherit the parent's
			childSub := subscriptionID
			if op.Resource.SubscriptionID != "" {
				childSub = op.Resource.SubscriptionID
			}
			children, err := fetchOperationsFor(ctx, getClient, childSub, op.Resource.ResourceGroup, op.Resource.Name)
			if err != nil {
				return []timing.Operation{}, fmt.Errorf("failed to fetch operations for child deployment %s/%s: %w", op.Resource.ResourceGroup, op.Resource.Name, err)
			}
			operations[i].Children = children
		}
	}
	return operations, nil
}

func operationFor(item *armresources.DeploymentOperation) (*timing.Operation, error) {
	if item == nil || item.Properties == nil {
		return nil, nil
	}
	var resource *timing.Resource
	if item.Properties.TargetResource != nil && item.Properties.TargetResource.ID != nil {
		resourceId, err := azcorearm.ParseResourceID(*item.Properties.TargetResource.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resource id: %w", err)
		}
		resource = &timing.Resource{
			ResourceType:   resourceId.ResourceType.String(),
			SubscriptionID: resourceId.SubscriptionID,
			ResourceGroup:  resourceId.ResourceGroupName,
			Name:           resourceId.Name,
		}
	}

	return &timing.Operation{
		OperationType:  string(*item.Properties.ProvisioningOperation),
		StartTimestamp: item.Properties.Timestamp.Format(time.RFC3339),
		Duration:       *item.Properties.Duration,
		Resource:       resource,
	}, nil
}
