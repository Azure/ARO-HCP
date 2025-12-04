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

import (
	"github.com/Azure/ARO-HCP/internal/api"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

type ControllerContainer interface {
	// TODO controllers are a concept that is at this scope and at lower scopes and sometimes you want to query all like it
	// TODO they look a lot like operations, though we can model them as a one-off to start.
	Controllers(hcpClusterID string) ResourceCRUD[api.Controller]
}

type HCPClusterCRUD interface {
	ResourceCRUD[api.HCPOpenShiftCluster]
	ControllerContainer

	ExternalAuth(hcpClusterID string) ExternalAuthsCRUD
	NodePools(hcpClusterID string) NodePoolsCRUD
}

type NodePoolsCRUD interface {
	ResourceCRUD[api.HCPOpenShiftClusterNodePool]
	ControllerContainer
}

type ExternalAuthsCRUD interface {
	ResourceCRUD[api.HCPOpenShiftClusterExternalAuth]
	ControllerContainer
}

type hcpClusterCRUD struct {
	*topLevelCosmosResourceCRUD[api.HCPOpenShiftCluster, HCPCluster]
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuth(hcpClusterID string) ExternalAuthsCRUD {
	return &externalAuthCRUD{
		nestedCosmosResourceCRUD: newNestedCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, ExternalAuth](h.containerClient, h.resourceType, h.subscriptionID, h.resourceGroupName, hcpClusterID, api.ExternalAuthResourceType),
	}
}

func (h *hcpClusterCRUD) NodePools(hcpClusterID string) NodePoolsCRUD {
	return &nodePoolsCRUD{
		nestedCosmosResourceCRUD: newNestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, NodePool](h.containerClient, h.resourceType, h.subscriptionID, h.resourceGroupName, hcpClusterID, api.NodePoolResourceType),
	}
}

func (h *hcpClusterCRUD) Controllers(hcpClusterID string) ResourceCRUD[api.Controller] {
	return NewControllerCRUD(h.containerClient, h.resourceType, h.subscriptionID, h.resourceGroupName, hcpClusterID)
}

type externalAuthCRUD struct {
	*nestedCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, ExternalAuth]
}

func (h *externalAuthCRUD) Controllers(hcpClusterID string) ResourceCRUD[api.Controller] {
	return NewControllerCRUD(h.containerClient, h.resourceType, h.subscriptionID, h.resourceGroupName, hcpClusterID)
}

type nodePoolsCRUD struct {
	*nestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, NodePool]
}

func (h *nodePoolsCRUD) Controllers(hcpClusterID string) ResourceCRUD[api.Controller] {
	return NewControllerCRUD(h.containerClient, h.resourceType, h.subscriptionID, h.resourceGroupName, hcpClusterID)
}

func NewControllerCRUD(
	containerClient *azcosmos.ContainerClient, parentResourceType azcorearm.ResourceType,
	subscriptionID, resourceGroupName, parentResourceName string) ResourceCRUD[api.Controller] {

	return newNestedCosmosResourceCRUD[api.Controller, Controller](containerClient, parentResourceType, subscriptionID, resourceGroupName, parentResourceName, api.ControllerResourceType)
}
