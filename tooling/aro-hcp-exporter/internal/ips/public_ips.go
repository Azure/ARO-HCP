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

package ips

import (
	"context"
	"fmt"

	internal "github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/utils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
)

type PublicIPAddressClient interface {
	NewListAllPager(options *armnetwork.PublicIPAddressesClientListAllOptions) *runtime.Pager[armnetwork.PublicIPAddressesClientListAllResponse]
}

type IPTag struct {
	ServiceTagType  string
	ServiceTagValue string
}

type PublicIPAddress struct {
	ID             string
	Name           string
	IPAddress      *string
	Location       *string
	ResourceGroup  string
	SubscriptionID string
	ServiceTags    []IPTag
}

// GetDummyPublicIPAddresses returns a dummy public IP address for testing purposes.
// This is useful, cause in our RedHat tenant we can not use Service Tags, and have no data to test with.
func GetDummyPublicIPAddresses() ([]PublicIPAddress, error) {
	return []PublicIPAddress{
		{
			ID:             "123",
			Name:           "test",
			IPAddress:      to.Ptr("123.45.67.89"),
			ResourceGroup:  "test-rg",
			SubscriptionID: "123",
			ServiceTags: []IPTag{
				{
					ServiceTagType:  "FirstPartyUsage",
					ServiceTagValue: "Dummy",
				},
			},
		},
	}, nil
}

// GetAllPublicIPAddresses retrieves all public IP addresses from Azure using the provided client.
// It handles paging automatically and returns all public IP addresses across all resource groups
// in the subscription associated with the client.
func GetAllPublicIPAddresses(ctx context.Context, client *armnetwork.PublicIPAddressesClient, region string) ([]PublicIPAddress, error) {
	var allIPs []PublicIPAddress

	pager := client.NewListAllPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of public IP addresses: %w", err)
		}

		for _, ip := range page.Value {
			if ip == nil {
				continue
			}

			if *ip.Location != region {
				continue
			}

			publicIP := PublicIPAddress{
				ID:          *ip.ID,
				Name:        *ip.Name,
				Location:    ip.Location,
				ServiceTags: extractServiceTags(ip.Properties.IPTags),
			}

			if ip.Properties != nil && ip.Properties.IPAddress != nil {
				publicIP.IPAddress = ip.Properties.IPAddress
			}

			if publicIP.ID != "" {
				resourceGroup, subscriptionID, err := extractResourceInfo(publicIP.ID)
				if err != nil {
					return nil, fmt.Errorf("failed to extract resource info: %w", err)
				}
				publicIP.ResourceGroup = resourceGroup
				publicIP.SubscriptionID = subscriptionID
			}

			allIPs = append(allIPs, publicIP)
		}
	}

	return allIPs, nil
}

func extractResourceInfo(resourceID string) (string, string, error) {
	parsedID, err := internal.ParseResourceID(resourceID)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse resource ID: %w", err)
	}

	return parsedID.ResourceGroupName, parsedID.SubscriptionID, nil
}

func extractServiceTags(ipTags []*armnetwork.IPTag) []IPTag {
	serviceTags := make([]IPTag, 0)

	for _, tag := range ipTags {
		if tag == nil {
			continue
		}

		serviceTags = append(serviceTags, IPTag{
			ServiceTagType:  *tag.IPTagType,
			ServiceTagValue: *tag.Tag,
		})
	}

	return serviceTags
}
