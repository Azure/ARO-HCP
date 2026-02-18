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
	"errors"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	return "", fmt.Errorf("subscription with name '%s' not found", subscriptionName)
}

// CreateResourceGroup creates a resource group
func CreateResourceGroup(
	ctx context.Context,
	resourceGroupsClient *armresources.ResourceGroupsClient,
	resourceGroupName string,
	location string,
	resourceGroupTTL time.Duration,
	timeout time.Duration,
) (*armresources.ResourceGroup, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateResourceGroup for resource group %s in location %s", timeout.Minutes(), resourceGroupName, location))
	defer cancel()

	if resourceGroupTTL < 60*time.Minute {
		return nil, fmt.Errorf("resourceGroupTTL must be at least an hour, got %v", resourceGroupTTL)
	}

	resourceGroup, err := resourceGroupsClient.CreateOrUpdate(ctx, resourceGroupName, armresources.ResourceGroup{
		Location: to.Ptr(location),
		Tags: map[string]*string{
			"e2e.aro-hcp-ci.redhat.com":         to.Ptr("true"),
			"deleteAfter.aro-hcp-ci.redhat.com": to.Ptr(fmt.Sprintf("%v", time.Now().Add(resourceGroupTTL).Format(time.RFC3339))),
		},
	}, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed to create resource group, caused by: %w, error: %w", context.Cause(ctx), err)
		}
		return nil, err
	}

	return &resourceGroup.ResourceGroup, nil
}

// ListAllExpiredResourceGroups returns all expired e2e resource groups
func ListAllExpiredResourceGroups(
	ctx context.Context,
	resourceGroupsClient *armresources.ResourceGroupsClient,
	now time.Time,
) ([]*armresources.ResourceGroup, error) {
	resourceGroupsPager := resourceGroupsClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
		Filter: ptr.To(`tagName eq 'e2e.aro-hcp-ci.redhat.com' and tagValue eq 'true'`),
	})

	allResourceGroups := []*armresources.ResourceGroup{}
	for resourceGroupsPager.More() {
		resourceGroupPage, err := resourceGroupsPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing resource groups: %w", err)
		}
		allResourceGroups = append(allResourceGroups, resourceGroupPage.Value...)
	}

	expiredResourceGroups := []*armresources.ResourceGroup{}
	for i := range allResourceGroups {
		currResourceGroup := allResourceGroups[i]
		expiryTime := currResourceGroup.Tags["deleteAfter.aro-hcp-ci.redhat.com"]
		if expiryTime == nil {
			continue
		}
		expiryTimeTime, err := time.Parse(time.RFC3339, *expiryTime)
		if err != nil {
			// TODO log
			continue
		}
		if expiryTimeTime.Before(now) {
			expiredResourceGroups = append(expiredResourceGroups, currResourceGroup)
		}
	}

	return expiredResourceGroups, nil
}

// DeleteResourceGroup deletes a resource group and waits for the operation to complete
func DeleteResourceGroup(
	ctx context.Context,
	resourceGroupsClient *armresources.ResourceGroupsClient,
	networkClientFactory *armnetwork.ClientFactory,
	resourceGroupName string,
	force bool,
	timeout time.Duration,
) error {

	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteResourceGroup for resource group %s", timeout.Minutes(), resourceGroupName))
	defer cancel()

	// detach any NSGs from subnets to avoid blocking deletion of the RG
	err := detachSubnetNSGs(ctx, networkClientFactory, resourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to detach NSGs from subnets in resource group %s: %w", resourceGroupName, err)
	}

	var opts *armresources.ResourceGroupsClientBeginDeleteOptions
	if force {
		opts = &armresources.ResourceGroupsClientBeginDeleteOptions{
			ForceDeletionTypes: to.Ptr("Microsoft.Compute/virtualMachines,Microsoft.Compute/virtualMachineScaleSets"),
		}
	}

	poller, err := resourceGroupsClient.BeginDelete(ctx, resourceGroupName, opts)
	if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return fmt.Errorf("failed to delete resource group, caused by: %w, error: %w", context.Cause(ctx), err)
        }
        return enrichErrorWithCorrelationID(err, "failed to begin deleting resource group")
    }

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return fmt.Errorf("failed to delete resource group, caused by: %w, error: %w", context.Cause(ctx), err)
        }
        return enrichErrorWithCorrelationID(err, fmt.Sprintf("failed waiting for resourcegroup=%q to finish deleting", resourceGroupName))
    }

	switch m := any(operationResult).(type) {
	case armresources.ResourceGroupsClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// enrichErrorWithCorrelationID extracts the Azure Correlation ID and wraps the error with it.
func enrichErrorWithCorrelationID(originalErr error, contextMsg string) error {
    var respErr *azcore.ResponseError
    if errors.As(originalErr, &respErr) && respErr.RawResponse != nil {
        corrID := respErr.RawResponse.Header.Get("x-ms-correlation-request-id")
        if corrID != "" {
            return fmt.Errorf("%s (CorrelationID: %s): %w", contextMsg, corrID, originalErr)
        }
    }
    // Fallback if no ID found
    return fmt.Errorf("%s: %w", contextMsg, originalErr)
}

// detach any NSGs from subnets to avoid blocking deletion of the RG
func detachSubnetNSGs(ctx context.Context, networkClientFactory *armnetwork.ClientFactory, resourceGroupName string) error {
	vnetClient := networkClientFactory.NewVirtualNetworksClient()
	subnetClient := networkClientFactory.NewSubnetsClient()

	vnetPager := vnetClient.NewListPager(resourceGroupName, nil)
	for vnetPager.More() {
		vnetPage, err := vnetPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing vnets in resource group %s: %w", resourceGroupName, err)
		}

		for _, vnet := range vnetPage.Value {
			if vnet == nil || vnet.Properties == nil || vnet.Properties.Subnets == nil || vnet.Name == nil {
				continue
			}

			subnetsPager := subnetClient.NewListPager(resourceGroupName, *vnet.Name, nil)
			for subnetsPager.More() {
				subnetPage, err := subnetsPager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("failed listing subnets in resource group %s: %w", resourceGroupName, err)
				}

				for _, subnet := range subnetPage.Value {
					if subnet == nil || subnet.Name == nil || subnet.Properties == nil || subnet.Properties.NetworkSecurityGroup == nil || subnet.Properties.NetworkSecurityGroup.ID == nil {
						continue
					}

					subnet.Properties.NetworkSecurityGroup = nil
					poller, err := subnetClient.BeginCreateOrUpdate(ctx, resourceGroupName, *vnet.Name, *subnet.Name, *subnet, &armnetwork.SubnetsClientBeginCreateOrUpdateOptions{})
					if err != nil {
						return fmt.Errorf("failed detaching NSG from subnet %s in resource group %s: %w", *subnet.Name, resourceGroupName, err)
					}

					_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
						Frequency: StandardPollInterval,
					})
					if err != nil {
						return fmt.Errorf("failed waiting for subnet %s in resource group %s to finish updating: %w", *subnet.Name, resourceGroupName, err)
					}
				}
			}
		}
	}

	return nil
}
