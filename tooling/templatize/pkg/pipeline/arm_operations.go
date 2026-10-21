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

package pipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// Operation describes an ARM deployment rollout operation taken to realize a template.
type Operation struct {
	// OperationType is what the operation did - known values: "Create", "Read", "EvaluateDeploymentOutput"
	OperationType string `json:"operationType"`

	// StartTimestamp is the time at which the operation started, formatted as RFC3339 date+time: 2025-11-05T13:16:20.624264+00:00
	StartTimestamp string `json:"startTimestamp"`
	// Duration is the time taken to run the operation, formatted as RFC3339 duration: PT3M12.9884364S
	Duration string `json:"duration"`

	// Resource defines the object of this operation.
	Resource *Resource `json:"resource,omitempty"`

	// Children holds the child operations when the resource is another deployment.
	Children []Operation `json:"children,omitempty"`
}

type Resource struct {
	// ResourceType is the resource provider and resource name, like "Microsoft.KeyVault/vaults".
	ResourceType string `json:"resourceType"`
	// SubscriptionID is the Azure subscription in which the resource exists.
	// Not serialized to avoid leaking sensitive identifiers into published artifacts.
	SubscriptionID string `json:"-"`
	// ResourceGroup is the Azure resource group in which the resource exists.
	ResourceGroup string `json:"resourceGroup"`
	// Name is the name of the resource.
	Name string `json:"name"`
}

// OperationsClientGetter returns a DeploymentOperationsClient for the given subscription ID.
// Implementations should cache clients to avoid redundant authentication.
type OperationsClientGetter func(subscriptionID string) (*armresources.DeploymentOperationsClient, error)

// NewCachedOperationsClientGetter creates an OperationsClientGetter that caches clients per subscription.
// The provided defaultClient is used for the given subscriptionID; clients for other subscriptions
// are created lazily using the provided credential.
func NewCachedOperationsClientGetter(subscriptionID string, defaultClient *armresources.DeploymentOperationsClient, cred azcore.TokenCredential, opts *azcorearm.ClientOptions) OperationsClientGetter {
	// mu guards the clients map, which may be accessed concurrently if
	// multiple deployment operations are fetched in parallel.
	var mu sync.Mutex
	clients := map[string]*armresources.DeploymentOperationsClient{
		subscriptionID: defaultClient,
	}
	return func(subID string) (*armresources.DeploymentOperationsClient, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := clients[subID]; ok {
			return c, nil
		}
		c, err := armresources.NewDeploymentOperationsClient(subID, cred, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to create operations client for subscription: %w", err)
		}
		clients[subID] = c
		return c, nil
	}
}

func DetermineOperationsForResourceGroupDeployment(getClient OperationsClientGetter, subscriptionID, resourceGroup, deploymentName string) DetailsProducer {
	return func(ctx context.Context) (*ExecutionDetails, error) {
		ops, err := fetchOperationsFor(ctx, getClient, subscriptionID, resourceGroup, deploymentName)
		if err != nil {
			return nil, err
		}
		return &ExecutionDetails{ARM: &ARMExecutionDetails{Operations: ops}}, nil
	}
}

func DetermineOperationsForSubscriptionDeployment(getClient OperationsClientGetter, subscriptionID, deploymentName string) DetailsProducer {
	return func(ctx context.Context) (*ExecutionDetails, error) {
		ops, err := fetchSubscriptionScopedOperationsFor(ctx, getClient, subscriptionID, deploymentName)
		if err != nil {
			return nil, err
		}
		return &ExecutionDetails{ARM: &ARMExecutionDetails{Operations: ops}}, nil
	}
}

// n.b. the Azure SDK for Go has unique types for the different types of pagers and no type constraint can be written to scope for pages of things containing lists of ops, so just copy-paste this instead of being clever

// fetchOperationsFor retrieves ARM deployment operations, handling both resource-group-scoped
// and subscription-scoped deployments. Recursively fetches child deployment operations,
// resolving the correct subscription client when a nested deployment crosses subscriptions.
func fetchOperationsFor(ctx context.Context, getClient OperationsClientGetter, subscriptionID, resourceGroup, deploymentName string) ([]Operation, error) {
	client, err := getClient(subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operations client for subscription: %w", err)
	}

	var operations []Operation
	if resourceGroup != "" {
		pager := client.NewListPager(resourceGroup, deploymentName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return []Operation{}, fmt.Errorf("failed to fetch operations: %w", err)
			}
			for _, item := range page.Value {
				op, err := operationFor(item)
				if err != nil {
					return []Operation{}, err
				}
				if op != nil {
					operations = append(operations, *op)
				}
			}
		}
	} else {
		pager := client.NewListAtSubscriptionScopePager(deploymentName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return []Operation{}, fmt.Errorf("failed to fetch operations: %w", err)
			}
			for _, item := range page.Value {
				op, err := operationFor(item)
				if err != nil {
					return []Operation{}, err
				}
				if op != nil {
					operations = append(operations, *op)
				}
			}
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
				return []Operation{}, fmt.Errorf("failed to fetch operations for child deployment %s/%s: %w", op.Resource.ResourceGroup, op.Resource.Name, err)
			}
			operations[i].Children = children
		}
	}
	return operations, nil
}

// operationFor converts an ARM DeploymentOperation into our internal Operation type.
// Extracts subscription ID from the resource's ARM ID to enable cross-subscription traversal.
func operationFor(item *armresources.DeploymentOperation) (*Operation, error) {
	if item == nil || item.Properties == nil {
		return nil, nil
	}
	var resource *Resource
	if item.Properties.TargetResource != nil && item.Properties.TargetResource.ID != nil {
		resourceId, err := azcorearm.ParseResourceID(*item.Properties.TargetResource.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resource id: %w", err)
		}
		resource = &Resource{
			ResourceType:   resourceId.ResourceType.String(),
			SubscriptionID: resourceId.SubscriptionID,
			ResourceGroup:  resourceId.ResourceGroupName,
			Name:           resourceId.Name,
		}
	}

	return &Operation{
		OperationType:  string(*item.Properties.ProvisioningOperation),
		StartTimestamp: item.Properties.Timestamp.Format(time.RFC3339),
		Duration:       *item.Properties.Duration,
		Resource:       resource,
	}, nil
}

// fetchSubscriptionScopedOperationsFor is a convenience entry point for subscription-scoped
// deployments (no resource group). Delegates to fetchOperationsFor with an empty resource group.
func fetchSubscriptionScopedOperationsFor(ctx context.Context, getClient OperationsClientGetter, subscriptionID, deploymentName string) ([]Operation, error) {
	return fetchOperationsFor(ctx, getClient, subscriptionID, "", deploymentName)
}
