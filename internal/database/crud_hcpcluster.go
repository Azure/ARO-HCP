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
	"path"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
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
	*nestedCosmosResourceCRUD[api.HCPOpenShiftCluster, HCPCluster]
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuth(hcpClusterName string) ExternalAuthsCRUD {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &externalAuthCRUD{
		nestedCosmosResourceCRUD: newNestedCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, ExternalAuth](
			h.containerClient,
			parentResourceID,
			api.ExternalAuthResourceType,
		),
	}
}

func (h *hcpClusterCRUD) NodePools(hcpClusterName string) NodePoolsCRUD {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &nodePoolsCRUD{
		nestedCosmosResourceCRUD: newNestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, NodePool](
			h.containerClient,
			parentResourceID,
			api.NodePoolResourceType),
	}
}

func (h *hcpClusterCRUD) Controllers(hcpClusterName string) ResourceCRUD[api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return NewControllerCRUD(h.containerClient, parentResourceID, api.ClusterControllerResourceType)
}

type externalAuthCRUD struct {
	*nestedCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, ExternalAuth]
}

func (h *externalAuthCRUD) Controllers(externalAuthName string) ResourceCRUD[api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			externalAuthName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, api.ExternalAuthControllerResourceType)
}

type nodePoolsCRUD struct {
	*nestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, NodePool]
}

func (h *nodePoolsCRUD) Controllers(nodePoolName string) ResourceCRUD[api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, api.NodePoolControllerResourceType)
}

func NewControllerCRUD(
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) ResourceCRUD[api.Controller] {

	return newNestedCosmosResourceCRUD[api.Controller, Controller](containerClient, parentResourceID, resourceType)
}
