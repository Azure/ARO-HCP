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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func UsesLoadBalancerEgress(cluster *coreapi.Cluster) bool {
	return len(cluster.CustomerProperties.Platform.OutboundType) == 0 || cluster.CustomerProperties.Platform.OutboundType == metadataapi.OutboundTypeLoadBalancer
}

func EgressPublicIPReferences(expected, confirmed []*azcorearm.ResourceID) coreapi.AzureMultiReference {
	confirmedIDs := make(map[string]bool, len(confirmed))
	for _, resourceID := range confirmed {
		if resourceID != nil {
			confirmedIDs[strings.ToLower(resourceID.String())] = true
		}
	}
	references := coreapi.AzureMultiReference{}
	for _, resourceID := range expected {
		if confirmedIDs[strings.ToLower(resourceID.String())] {
			references.AzureResources = append(references.AzureResources, resourceID)
		} else {
			references.PendingAzureResources = append(references.PendingAzureResources, resourceID)
		}
	}
	return references
}

// egressPublicIPCount is the number of node-egress public IPs per cluster, not per node pool.
const egressPublicIPCount = 9

// egressPublicIPName returns "{clusterServiceID}-{index}". clusterServiceID is the nonempty
// cluster identity's final path segment, and index must be in [0, egressPublicIPCount).
func egressPublicIPName(clusterServiceID string, index int) string {
	return fmt.Sprintf("%s-%d", clusterServiceID, index)
}

// EgressPublicIPResourceIDs returns the expected node-egress public IP IDs for a
// cluster using LoadBalancer outbound (including its legacy default), or nil
// while naming prerequisites are unavailable or outbound is not LoadBalancer.
// Invalid ARM IDs return an error.
func EgressPublicIPResourceIDs(cluster *coreapi.Cluster) ([]*azcorearm.ResourceID, error) {
	if !UsesLoadBalancerEgress(cluster) || cluster.ID == nil || cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.Path()) == 0 || len(cluster.CustomerProperties.Platform.ManagedResourceGroup) == 0 {
		return nil, nil
	}
	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	resourceIDs := make([]*azcorearm.ResourceID, 0, egressPublicIPCount)
	for index := 0; index < egressPublicIPCount; index++ {
		expectedID := scope + "/providers/Microsoft.Network/publicIPAddresses/" + egressPublicIPName(cluster.ServiceProviderProperties.ClusterServiceID.ID(), index)
		resourceID, err := azcorearm.ParseResourceID(expectedID)
		if err != nil {
			return nil, err
		}
		resourceIDs = append(resourceIDs, resourceID)
	}
	return resourceIDs, nil
}
