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

package coreapitesting

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func ServiceProviderForCluster(cluster *coreapi.Cluster, pending, final *metadataapi.InternalID) *coreapi.ServiceProviderCluster {
	if cluster == nil {
		return nil
	}
	provider := &coreapi.ServiceProviderCluster{
		PendingClusterServiceID: pending.DeepCopy(),
		ClusterServiceID:        final.DeepCopy(),
	}
	if cluster.ID != nil {
		provider.CosmosMetadata = coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String() + "/serviceProviderClusters/default")), PartitionKey: cluster.ID.SubscriptionID}
	}
	return provider
}
