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
	"fmt"
	"path"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type ControllerContainer interface {
	// TODO controllers are a concept that is at this scope and at lower scopes and sometimes you want to query all like it
	// TODO they look a lot like operations, though we can model them as a one-off to start.
	Controllers(hcpClusterID string) ResourceCRUD[api.Controller, *api.Controller]
}

type OperationCRUD interface {
	ResourceCRUD[api.Operation, *api.Operation]

	// ListActiveOperations returns an iterator that searches for asynchronous operation documents
	// with a non-terminal status in the "Resources" container under the given partition key. The
	// options argument can further limit the search to documents that match the provided values.
	//
	// Note that ListActiveOperations does not perform the search, but merely prepares an iterator
	// to do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) DBClientIterator[api.Operation]
}

type operationCRUD struct {
	ResourceCRUD[api.Operation, *api.Operation]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
}

func NewOperationCRUD(containerClient *azcosmos.ContainerClient, subscriptionID string, registerer prometheus.Registerer) OperationCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	parentResourceID := api.Must(azcorearm.ParseResourceID(path.Join(parts...)))

	raw := newCosmosResourceCRUD[api.Operation, *api.Operation, GenericDocument[api.Operation]](containerClient, parentResourceID, api.OperationStatusResourceType)
	return &operationCRUD{
		ResourceCRUD:     NewInstrumentedCRUD[api.Operation, *api.Operation](raw, "Operation", registerer),
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
	}
}

var _ OperationCRUD = &operationCRUD{}

func (d *operationCRUD) ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) DBClientIterator[api.Operation] {
	var queryOptions azcosmos.QueryOptions

	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND LENGTH(c.resourceID) > 0 "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		api.OperationStatusResourceType.String(),
		arm.ProvisioningStateSucceeded,
		arm.ProvisioningStateFailed,
		arm.ProvisioningStateCanceled)

	if options != nil {
		if options.Request != nil {
			query += " AND c.properties.request = @request"
			queryParameter := azcosmos.QueryParameter{
				Name:  "@request",
				Value: string(*options.Request),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}

		if options.ExternalID != nil {
			query += " AND "
			const resourceFilter = "STRINGEQUALS(c.properties.externalId, @externalId, true)"
			if options.IncludeNestedResources {
				const nestedResourceFilter = "STARTSWITH(c.properties.externalId, CONCAT(@externalId, \"/\"), true)"
				query += fmt.Sprintf("(%s OR %s)", resourceFilter, nestedResourceFilter)
			} else {
				query += resourceFilter
			}
			queryParameter := azcosmos.QueryParameter{
				Name:  "@externalId",
				Value: options.ExternalID.String(),
			}
			queryOptions.QueryParameters = append(queryOptions.QueryParameters, queryParameter)
		}
	}

	pager := d.containerClient.NewQueryItemsPager(query, NewPartitionKey(d.parentResourceID.SubscriptionID), &queryOptions)
	return newQueryResourcesIterator[api.Operation, GenericDocument[api.Operation]](pager)
}

type HCPClusterCRUD interface {
	ResourceCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster]
	ControllerContainer
	ManagementClusterContentContainer

	ExternalAuth(hcpClusterID string) ExternalAuthsCRUD
	NodePools(hcpClusterID string) NodePoolsCRUD
}

func NewHCPClusterCRUD(containerClient *azcosmos.ContainerClient, subscriptionID, resourceGroupName string, registerer prometheus.Registerer) HCPClusterCRUD {
	var parentResourceID *azcorearm.ResourceID
	if len(resourceGroupName) > 0 {
		parentResourceID = api.Must(api.ToResourceGroupResourceID(subscriptionID, resourceGroupName))
	} else {
		parentResourceID = api.Must(arm.ToSubscriptionResourceID(subscriptionID))
	}

	raw := newCosmosResourceCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster, GenericDocument[api.HCPOpenShiftCluster]](containerClient, parentResourceID, api.ClusterResourceType)
	return &hcpClusterCRUD{
		ResourceCRUD:     NewInstrumentedCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster](raw, "HCPOpenShiftCluster", registerer),
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
		resourceType:     api.ClusterResourceType,
		registerer:       registerer,
	}
}

type NodePoolsCRUD interface {
	ResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool]
	ControllerContainer
	ManagementClusterContentContainer
}

type ExternalAuthsCRUD interface {
	ResourceCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth]
	ControllerContainer
}

type hcpClusterCRUD struct {
	ResourceCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
	registerer       prometheus.Registerer
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

	raw := newCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth, GenericDocument[api.HCPOpenShiftClusterExternalAuth]](
		h.containerClient,
		parentResourceID,
		api.ExternalAuthResourceType,
	)
	return &externalAuthCRUD{
		ResourceCRUD:     NewInstrumentedCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth](raw, "HCPOpenShiftClusterExternalAuth", h.registerer),
		containerClient:  h.containerClient,
		parentResourceID: parentResourceID,
		resourceType:     api.ExternalAuthResourceType,
		registerer:       h.registerer,
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

	raw := newCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, GenericDocument[api.HCPOpenShiftClusterNodePool]](
		h.containerClient,
		parentResourceID,
		api.NodePoolResourceType)
	return &nodePoolsCRUD{
		ResourceCRUD:     NewInstrumentedCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool](raw, "HCPOpenShiftClusterNodePool", h.registerer),
		containerClient:  h.containerClient,
		parentResourceID: parentResourceID,
		resourceType:     api.NodePoolResourceType,
		registerer:       h.registerer,
	}
}

func (h *hcpClusterCRUD) Controllers(hcpClusterName string) ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return NewControllerCRUD(h.containerClient, parentResourceID, api.ClusterControllerResourceType, h.registerer)
}

func (h *hcpClusterCRUD) ManagementClusterContents(hcpClusterName string) ResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return NewCosmosResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent, GenericDocument[api.ManagementClusterContent]](
		h.containerClient,
		parentResourceID,
		api.ClusterScopedManagementClusterContentResourceType,
		"ManagementClusterContent",
		h.registerer,
	)
}

type externalAuthCRUD struct {
	ResourceCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
	registerer       prometheus.Registerer
}

func (h *externalAuthCRUD) Controllers(externalAuthName string) ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			externalAuthName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, api.ExternalAuthControllerResourceType, h.registerer)
}

type nodePoolsCRUD struct {
	ResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
	registerer       prometheus.Registerer
}

func (h *nodePoolsCRUD) Controllers(nodePoolName string) ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, api.NodePoolControllerResourceType, h.registerer)
}

func (h *nodePoolsCRUD) ManagementClusterContents(nodePoolName string) ResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewCosmosResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent, GenericDocument[api.ManagementClusterContent]](
		h.containerClient,
		parentResourceID,
		api.NodePoolScopedManagementClusterContentResourceType,
		"ManagementClusterContent",
		h.registerer,
	)
}

func NewControllerCRUD(
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType, registerer prometheus.Registerer) ResourceCRUD[api.Controller, *api.Controller] {

	return NewCosmosResourceCRUD[api.Controller, *api.Controller, GenericDocument[api.Controller]](containerClient, parentResourceID, resourceType, "Controller", registerer)
}
