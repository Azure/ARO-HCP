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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/davecgh/go-spew/spew"
)

func GetSubscriptionID(ctx context.Context, subscriptionClient *armsubscriptions.Client, subscriptionName string) (string, error) {
	pager := subscriptionClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, sub := range page.Value {
			if *sub.DisplayName == subscriptionName {
				return *sub.SubscriptionID, nil
			}
		}
	}
	return "", fmt.Errorf("subscription with name %s not found", subscriptionName)
}

// CreateResourceGroup creates a resource group
func CreateResourceGroup(
	ctx context.Context,
	resourceGroupsClient *armresources.ResourceGroupsClient,
	resourceGroupName string,
	location string,
	timeout time.Duration,
) (*armresources.ResourceGroup, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resourceGroup, err := resourceGroupsClient.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Location: to.Ptr(location),
	}, nil)
	if err != nil {
		return nil, err
	}

	return &resourceGroup.ResourceGroup, nil
}

// DeleteResourceGroup deletes a resource group and waits for the operation to complete
func DeleteResourceGroup(
	ctx context.Context,
	resourceGroupsClient *armresources.ResourceGroupsClient,
	resourceGroupName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := resourceGroupsClient.BeginDelete(ctx, resourceGroupName, nil)
	if err != nil {
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return fmt.Errorf("failed waiting for resourcegroup=%q to finish deleting: %w", resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case armresources.ResourceGroupsClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}
