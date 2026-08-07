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

package databasemutationhelpers

import (
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func NewCosmosCRUD[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType]](t *testing.T, cosmosClient corecosmosstorage.ResourcesDBClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer] {
	switch {
	case strings.EqualFold(resourceType.String(), coreapi.ClusterControllerResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).Controllers(parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])
	case strings.EqualFold(resourceType.String(), coreapi.ExternalAuthControllerResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).ExternalAuth(parentResourceID.Parent.Name).Controllers(parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])
	case strings.EqualFold(resourceType.String(), coreapi.NodePoolControllerResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).NodePools(parentResourceID.Parent.Name).Controllers(parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])

	case strings.EqualFold(resourceType.String(), coreapi.ClusterResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])
	case strings.EqualFold(resourceType.String(), coreapi.ExternalAuthResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).ExternalAuth(parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])
	case strings.EqualFold(resourceType.String(), coreapi.NodePoolResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).NodePools(parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])

	case strings.EqualFold(resourceType.String(), coreapi.OperationStatusResourceType.String()):
		return any(cosmosClient.Operations(parentResourceID.SubscriptionID)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])

	case strings.EqualFold(resourceType.String(), coreapi.ServiceProviderClusterResourceType.String()):
		return any(cosmosClient.ServiceProviderClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName, parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])

	case strings.EqualFold(resourceType.String(), coreapi.ServiceProviderNodePoolResourceType.String()):
		return any(cosmosClient.ServiceProviderNodePools(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName, parentResourceID.Parent.Name, parentResourceID.Name)).(cosmosstorageutils.ResourceCRUD[InternalAPIType, InternalAPITypePointer])

	default:
		t.Fatalf("unsupported resource type and parent: %q under %v", resourceType, parentResourceID.ResourceType.String())
	}

	panic("unreachable")
}
