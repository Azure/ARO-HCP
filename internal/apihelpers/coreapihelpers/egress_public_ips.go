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

package coreapihelpers

import (
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func UsesLoadBalancerEgress(cluster *coreapi.Cluster) bool {
	return cluster.CustomerProperties.Platform.OutboundType == "" || cluster.CustomerProperties.Platform.OutboundType == metadataapi.OutboundTypeLoadBalancer
}

func EgressPublicIPResourceIDs(cluster *coreapi.Cluster) ([]*azcorearm.ResourceID, error) {
	if !UsesLoadBalancerEgress(cluster) || cluster.ID == nil || cluster.ServiceProviderProperties.ClusterServiceID == nil || cluster.ServiceProviderProperties.ClusterServiceID.ID() == "" || cluster.CustomerProperties.Platform.ManagedResourceGroup == "" {
		return nil, nil
	}
	resourceIDs := make([]*azcorearm.ResourceID, 0, 9)
	for index := 0; index < 9; index++ {
		resourceID, err := azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/publicIPAddresses/%s-%d", cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup, cluster.ServiceProviderProperties.ClusterServiceID.ID(), index))
		if err != nil {
			return nil, err
		}
		resourceIDs = append(resourceIDs, resourceID)
	}
	return resourceIDs, nil
}
