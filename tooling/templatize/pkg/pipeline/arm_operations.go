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
	"time"

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
	// ResourceGroup is the Azure resource group in which the resource exists.
	ResourceGroup string `json:"resourceGroup"`
	// Name is the name of the resource.
	Name string `json:"name"`
}

func DetermineOperationsForResourceGroupDeployment(client *armresources.DeploymentOperationsClient, resourceGroup, deploymentName string) DetailsProducer {
	return func(ctx context.Context) (*ExecutionDetails, error) {
		ops, err := fetchOperationsFor(ctx, client, resourceGroup, deploymentName)
		if err != nil {
			return nil, err
		}
		return &ExecutionDetails{ARM: &ARMExecutionDetails{Operations: ops}}, nil
	}
}

func DetermineOperationsForSubscriptionDeployment(client *armresources.DeploymentOperationsClient, deploymentName string) DetailsProducer {
	return func(ctx context.Context) (*ExecutionDetails, error) {
		ops, err := fetchSubscriptionScopedOperationsFor(ctx, client, deploymentName)
		if err != nil {
			return nil, err
		}
		return &ExecutionDetails{ARM: &ARMExecutionDetails{Operations: ops}}, nil
	}
}

// n.b. the Azure SDK for Go has unique types for the different types of pagers and no type constraint can be written to scope for pages of things containing lists of ops, so just copy-paste this instead of being clever

func fetchOperationsFor(ctx context.Context, client *armresources.DeploymentOperationsClient, resourceGroup, deploymentName string) ([]Operation, error) {
	var operations []Operation
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

	for i, op := range operations {
		if op.Resource == nil {
			continue
		}
		if strings.EqualFold(op.Resource.ResourceType, "Microsoft.Resources/deployments") {
			children, err := fetchOperationsFor(ctx, client, op.Resource.ResourceGroup, op.Resource.Name)
			if err != nil {
				return []Operation{}, fmt.Errorf("failed to fetch operations for child deployment %s/%s: %w", op.Resource.ResourceGroup, op.Resource.Name, err)
			}
			operations[i].Children = children
		}
	}
	return operations, nil
}

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
			ResourceType:  resourceId.ResourceType.String(),
			ResourceGroup: resourceId.ResourceGroupName,
			Name:          resourceId.Name,
		}
	}

	return &Operation{
		OperationType:  string(*item.Properties.ProvisioningOperation),
		StartTimestamp: item.Properties.Timestamp.Format(time.RFC3339),
		Duration:       *item.Properties.Duration,
		Resource:       resource,
	}, nil
}

func fetchSubscriptionScopedOperationsFor(ctx context.Context, client *armresources.DeploymentOperationsClient, deploymentName string) ([]Operation, error) {
	var operations []Operation
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

	for i, op := range operations {
		if op.Resource == nil {
			continue
		}
		if strings.EqualFold(op.Resource.ResourceType, "Microsoft.Resources/deployments") {
			children, err := fetchSubscriptionScopedOperationsFor(ctx, client, op.Resource.Name)
			if err != nil {
				return []Operation{}, fmt.Errorf("failed to fetch operations for child deployment %s/%s: %w", op.Resource.ResourceGroup, op.Resource.Name, err)
			}
			operations[i].Children = children
		}
	}
	return operations, nil
}
