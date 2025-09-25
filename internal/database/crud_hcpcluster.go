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

package database

import "github.com/Azure/ARO-HCP/internal/api"

type HCPClusterCRUD interface {
	ResourceCRUD[HCPCluster]

	ExternalAuth(hcpClusterID string) ResourceCRUD[ExternalAuth]
	NodePools(hcpClusterID string) ResourceCRUD[NodePool]
}

type hcpClusterCRUD struct {
	*topLevelCosmosResourceCRUD[HCPCluster]
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuth(hcpClusterID string) ResourceCRUD[ExternalAuth] {
	return newNestedCosmosResourceCRUD[ExternalAuth](h.topLevelCosmosResourceCRUD, h.subscriptionID, h.resourceGroupName, hcpClusterID, api.ExternalAuthResourceType)
}

func (h *hcpClusterCRUD) NodePools(hcpClusterID string) ResourceCRUD[NodePool] {
	return newNestedCosmosResourceCRUD[NodePool](h.topLevelCosmosResourceCRUD, h.subscriptionID, h.resourceGroupName, hcpClusterID, api.NodePoolResourceType)
}
