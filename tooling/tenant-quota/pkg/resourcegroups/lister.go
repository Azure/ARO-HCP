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

package resourcegroups

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// CollectorConfig parameterizes the resource group collector so different
// tag-based resource group monitors can reuse the same collection logic.
type CollectorConfig struct {
	TagFilter    string // ARM filter string for the ListPager (e.g. "tagName eq 'foo' and tagValue eq 'bar'")
	TTLTagKey    string // tag key holding the RFC3339 expiry timestamp
	MetricPrefix string // prefix for Prometheus metric names (e.g. "e2e_resource_group")
	Name         string // human-readable name for log messages (e.g. "e2e resource group")
}

// E2ECollectorConfig is the configuration for monitoring E2E-tagged
// resource groups created by CI test jobs.
var E2ECollectorConfig = CollectorConfig{
	TagFilter:    "tagName eq 'e2e.aro-hcp-ci.redhat.com' and tagValue eq 'true'",
	TTLTagKey:    "deleteAfter.aro-hcp-ci.redhat.com",
	MetricPrefix: "e2e_resource_group",
	Name:         "e2e resource group",
}

// ResourceGroupLister abstracts Azure RG listing for testability.
type ResourceGroupLister interface {
	ListResourceGroups(ctx context.Context, subscriptionID string) ([]*armresources.ResourceGroup, error)
}

// AzureResourceGroupLister is the production implementation using the ARM SDK.
type AzureResourceGroupLister struct {
	cred   *azidentity.ClientSecretCredential
	filter string
}

func (a *AzureResourceGroupLister) ListResourceGroups(ctx context.Context, subscriptionID string) ([]*armresources.ResourceGroup, error) {
	client, err := armresources.NewResourceGroupsClient(subscriptionID, a.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create resource groups client: %w", err)
	}

	pager := client.NewListPager(&armresources.ResourceGroupsClientListOptions{
		Filter: to.Ptr(a.filter),
	})

	var result []*armresources.ResourceGroup
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list resource groups: %w", err)
		}
		result = append(result, page.Value...)
	}
	return result, nil
}
