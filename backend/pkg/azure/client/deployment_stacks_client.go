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

package client

//go:generate $MOCKGEN -typed -source=deployment_stacks_client.go -destination=mock_deployment_stacks_client.go -package client DeploymentStacksClient

import (
	"context"

	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"
)

// DeploymentStacksClient is an interface that mirrors armdeploymentstacks.Client.
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/resources/armdeploymentstacks).
// Only the methods the backend needs are declared. If the backend needs more of the
// SDK client's methods, add them here to keep parity.
type DeploymentStacksClient interface {
	GetAtResourceGroup(ctx context.Context, resourceGroupName string, deploymentStackName string, options *armdeploymentstacks.ClientGetAtResourceGroupOptions) (armdeploymentstacks.ClientGetAtResourceGroupResponse, error)
	GetAtSubscription(ctx context.Context, deploymentStackName string, options *armdeploymentstacks.ClientGetAtSubscriptionOptions) (armdeploymentstacks.ClientGetAtSubscriptionResponse, error)
	GetAtManagementGroup(ctx context.Context, managementGroupID string, deploymentStackName string, options *armdeploymentstacks.ClientGetAtManagementGroupOptions) (armdeploymentstacks.ClientGetAtManagementGroupResponse, error)

	BeginCreateOrUpdateAtResourceGroup(ctx context.Context, resourceGroupName string, deploymentStackName string, deploymentStack armdeploymentstacks.DeploymentStack, options *armdeploymentstacks.ClientBeginCreateOrUpdateAtResourceGroupOptions) (*azruntime.Poller[armdeploymentstacks.ClientCreateOrUpdateAtResourceGroupResponse], error)
	BeginCreateOrUpdateAtSubscription(ctx context.Context, deploymentStackName string, deploymentStack armdeploymentstacks.DeploymentStack, options *armdeploymentstacks.ClientBeginCreateOrUpdateAtSubscriptionOptions) (*azruntime.Poller[armdeploymentstacks.ClientCreateOrUpdateAtSubscriptionResponse], error)
	BeginCreateOrUpdateAtManagementGroup(ctx context.Context, managementGroupID string, deploymentStackName string, deploymentStack armdeploymentstacks.DeploymentStack, options *armdeploymentstacks.ClientBeginCreateOrUpdateAtManagementGroupOptions) (*azruntime.Poller[armdeploymentstacks.ClientCreateOrUpdateAtManagementGroupResponse], error)

	BeginDeleteAtResourceGroup(ctx context.Context, resourceGroupName string, deploymentStackName string, options *armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions) (*azruntime.Poller[armdeploymentstacks.ClientDeleteAtResourceGroupResponse], error)
	BeginDeleteAtSubscription(ctx context.Context, deploymentStackName string, options *armdeploymentstacks.ClientBeginDeleteAtSubscriptionOptions) (*azruntime.Poller[armdeploymentstacks.ClientDeleteAtSubscriptionResponse], error)
	BeginDeleteAtManagementGroup(ctx context.Context, managementGroupID string, deploymentStackName string, options *armdeploymentstacks.ClientBeginDeleteAtManagementGroupOptions) (*azruntime.Poller[armdeploymentstacks.ClientDeleteAtManagementGroupResponse], error)
}

var _ DeploymentStacksClient = (*armdeploymentstacks.Client)(nil)
