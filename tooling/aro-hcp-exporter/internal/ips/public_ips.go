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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// PublicIPAddressClient is an abstraction for Azure Public IP Address client operations.
// This interface allows for dependency injection and easier testing.
type PublicIPAddressClient interface {
	// NewListAllPager creates a pager for listing all public IP addresses in a subscription.
	NewListAllPager(options *armnetwork.PublicIPAddressesClientListAllOptions) *runtime.Pager[armnetwork.PublicIPAddressesClientListAllResponse]
}

// PublicIPAddress represents a simplified view of an Azure Public IP Address.
type PublicIPAddress struct {
	// ID is the resource ID of the public IP address
	ID string
	// Name is the name of the public IP address
	Name string
	// IPAddress is the actual IP address (may be nil if not allocated)
	IPAddress *string
	// Location is the Azure region where the resource is located
	Location *string
	// ResourceGroup is the resource group name (extracted from ID)
	ResourceGroup string
	// SubscriptionID is the subscription ID (extracted from ID)
	SubscriptionID string
	// Tags are the Azure tags associated with the public IP address
	Tags map[string]string
}

// GetAllPublicIPAddresses retrieves all public IP addresses from Azure using the provided client.
// It handles paging automatically and returns all public IP addresses across all resource groups
// in the subscription associated with the client.
func GetAllPublicIPAddresses(ctx context.Context, client PublicIPAddressClient) ([]PublicIPAddress, error) {
	var allIPs []PublicIPAddress

	// Create a pager to handle pagination
	pager := client.NewListAllPager(nil)

	// Iterate through all pages
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of public IP addresses: %w", err)
		}

		// Process each public IP address in the current page
		for _, ip := range page.Value {
			if ip == nil {
				continue
			}

			publicIP := PublicIPAddress{
				ID:       getStringValue(ip.ID),
				Name:     getStringValue(ip.Name),
				Location: ip.Location,
				Tags:     extractTags(ip.Tags),
			}

			// Extract IP address if available
			if ip.Properties != nil && ip.Properties.IPAddress != nil {
				publicIP.IPAddress = ip.Properties.IPAddress
			}

			// Extract resource group and subscription from the resource ID
			if publicIP.ID != "" {
				publicIP.ResourceGroup, publicIP.SubscriptionID = extractResourceInfo(publicIP.ID)
			}

			allIPs = append(allIPs, publicIP)
		}
	}

	return allIPs, nil
}

// getStringValue safely extracts a string value from a pointer, returning empty string if nil.
func getStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// extractResourceInfo extracts resource group and subscription ID from an Azure resource ID.
// Azure resource IDs follow the pattern:
// /subscriptions/{subscription-id}/resourceGroups/{resource-group}/providers/{provider}/{resource-type}/{resource-name}
func extractResourceInfo(resourceID string) (resourceGroup, subscriptionID string) {
	// Simple parsing - in production, you might want to use a more robust parser
	// This assumes the standard Azure resource ID format
	parts := splitResourceID(resourceID)

	if len(parts) >= 2 && parts[0] == "subscriptions" {
		subscriptionID = parts[1]
	}

	if len(parts) >= 4 && parts[2] == "resourceGroups" {
		resourceGroup = parts[3]
	}

	return resourceGroup, subscriptionID
}

// splitResourceID splits an Azure resource ID by '/' and filters out empty strings.
func splitResourceID(id string) []string {
	var parts []string
	start := 0

	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			if i > start {
				parts = append(parts, id[start:i])
			}
			start = i + 1
		}
	}

	if start < len(id) {
		parts = append(parts, id[start:])
	}

	return parts
}

// extractTags converts Azure tags from the SDK format to a map[string]string.
// Azure SDK tags are map[string]*string, so we need to dereference the pointers safely.
func extractTags(azureTags map[string]*string) map[string]string {
	if azureTags == nil {
		return make(map[string]string)
	}

	tags := make(map[string]string, len(azureTags))
	for key, value := range azureTags {
		if value != nil {
			tags[key] = *value
		}
	}

	return tags
}
