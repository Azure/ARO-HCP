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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

func NewCosmosCRUD[InternalAPIType any](t *testing.T, cosmosClient database.DBClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) database.ResourceCRUD[InternalAPIType] {
	switch {
	case strings.EqualFold(resourceType.String(), api.ClusterControllerResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).Controllers(parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])
	case strings.EqualFold(resourceType.String(), api.ExternalAuthControllerResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).ExternalAuth(parentResourceID.Parent.Name).Controllers(parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])
	case strings.EqualFold(resourceType.String(), api.NodePoolControllerResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).NodePools(parentResourceID.Parent.Name).Controllers(parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])

	case strings.EqualFold(resourceType.String(), api.ClusterResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName)).(database.ResourceCRUD[InternalAPIType])
	case strings.EqualFold(resourceType.String(), api.ExternalAuthResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).ExternalAuth(parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])
	case strings.EqualFold(resourceType.String(), api.NodePoolResourceType.String()):
		return any(cosmosClient.HCPClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName).NodePools(parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])

	case strings.EqualFold(resourceType.String(), api.OperationStatusResourceType.String()):
		return any(cosmosClient.Operations(parentResourceID.SubscriptionID)).(database.ResourceCRUD[InternalAPIType])

	case strings.EqualFold(resourceType.String(), api.ServiceProviderClusterResourceType.String()):
		return any(cosmosClient.ServiceProviderClusters(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName, parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])

	case strings.EqualFold(resourceType.String(), api.ServiceProviderNodePoolResourceType.String()):
		return any(cosmosClient.ServiceProviderNodePools(parentResourceID.SubscriptionID, parentResourceID.ResourceGroupName, parentResourceID.Parent.Name, parentResourceID.Name)).(database.ResourceCRUD[InternalAPIType])

	default:
		t.Fatalf("unsupported resource type and parent: %q under %v", resourceType, parentResourceID.ResourceType.String())
	}

	panic("unreachable")
}
