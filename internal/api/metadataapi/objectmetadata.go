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

package metadataapi

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ObjectMetadata provides per-document identity for the cosmosResourceSnapshots
// Kusto table. It is emitted as a structured log field alongside the document content.
type ObjectMetadata struct {
	CosmosContainer string `json:"cosmosContainer"`
	SubscriptionID  string `json:"subscriptionID"`
	ResourceGroup   string `json:"resourceGroup"`
	ResourceType    string `json:"resourceType"`
	ResourceName    string `json:"resourceName"`
	ResourceID      string `json:"resourceID"`
	// ClusterResourceID is the full resource ID of the logical parent HCP cluster.
	// It is empty when the resource is not part of an HCP cluster.
	ClusterResourceID string `json:"clusterResourceID"`
}

// ObjectMetadataForResourceID builds ObjectMetadata from an ARM resource ID.
func ObjectMetadataForResourceID(container string, resourceID *azcorearm.ResourceID) ObjectMetadata {
	if resourceID == nil {
		return ObjectMetadata{CosmosContainer: container}
	}
	return ObjectMetadata{
		CosmosContainer:   container,
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroup:     resourceID.ResourceGroupName,
		ResourceType:      resourceID.ResourceType.String(),
		ResourceName:      resourceID.Name,
		ResourceID:        resourceID.String(),
		ClusterResourceID: ClusterNameFromResourceID(resourceID),
	}
}
