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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/aks"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
)

func LookupSubscriptionID(ctx context.Context, subscriptionName string) (string, error) {
	// Create a new Azure identity client
	cred, err := azauth.GetAzureTokenCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to obtain a credential: %v", err)
	}

	// Create a new subscriptions client
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create subscriptions client: %v", err)
	}

	// List subscriptions and find the one with the matching name
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get next page of subscriptions: %v", err)
		}
		for _, sub := range page.Value {
			if sub.DisplayName != nil && *sub.DisplayName == subscriptionName {
				return *sub.SubscriptionID, nil
			}
		}
	}

	return "", fmt.Errorf("subscription with name %q not found", subscriptionName)
}

func KubeConfig(ctx context.Context, subscriptionID, resourceGroup, aksClusterName string) (string, error) {
	if aksClusterName == "" {
		return "", nil
	}

	// Get Kubeconfig
	kubeconfigPath, err := aks.GetKubeConfig(ctx, subscriptionID, resourceGroup, aksClusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Make sure we have cluster admin
	err = aks.EnsureClusterAdmin(ctx, kubeconfigPath, subscriptionID, resourceGroup, aksClusterName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to ensure cluster admin role: %w", err)
	}
	return kubeconfigPath, nil
}

type ExecutionTarget interface {
	GetSubscriptionID() string
	GetResourceGroup() string
	GetRegion() string
}

type executionTargetImpl struct {
	subscriptionName string
	subscriptionID   string
	resourceGroup    string
	region           string
}

func (target *executionTargetImpl) GetSubscriptionID() string {
	return target.subscriptionID
}

func (target *executionTargetImpl) GetResourceGroup() string {
	return target.resourceGroup
}

func (target *executionTargetImpl) GetRegion() string {
	return target.region
}
