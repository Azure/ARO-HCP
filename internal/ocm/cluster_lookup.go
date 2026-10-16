// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ocm

import (
	"context"
	"fmt"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
)

// FindClusterByAzureInfo locates an already-created CS cluster for adoption.
// DNS allocation and create dispatch use the same lookup to preserve existing names.
func FindClusterByAzureInfo(ctx context.Context, client ClusterServiceClientSpec, subscriptionID, resourceGroupName, resourceName, tenantID, managedResourceGroupName string) (*arohcpv1alpha1.Cluster, error) {
	wantSub, wantRG, wantName := strings.ToLower(subscriptionID), strings.ToLower(resourceGroupName), strings.ToLower(resourceName)
	search := fmt.Sprintf("azure.subscription_id = '%s' and azure.resource_group_name = '%s' and azure.resource_name = '%s' and azure.tenant_id = '%s' and azure.managed_resource_group_name = '%s'", wantSub, wantRG, wantName, tenantID, managedResourceGroupName)
	iterator := client.ListClusters(search)
	var match *arohcpv1alpha1.Cluster
	for cluster := range iterator.Items(ctx) {
		azure := cluster.Azure()
		if azure == nil || azure.SubscriptionID() != wantSub || azure.ResourceGroupName() != wantRG || azure.ResourceName() != wantName || azure.TenantID() != tenantID || azure.ManagedResourceGroupName() != managedResourceGroupName {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("cluster service returned multiple clusters for one Azure resource: subscription_id=%q resource_group=%q resource_name=%q tenant_id=%q managed_resource_group=%q", wantSub, wantRG, wantName, tenantID, managedResourceGroupName)
		}
		match = cluster
	}
	if err := iterator.GetError(); err != nil {
		return nil, err
	}
	return match, nil
}
