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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

type ControllerContainer interface {
	// TODO controllers are a concept that is at this scope and at lower scopes and sometimes you want to query all like it
	// TODO they look a lot like operations, though we can model them as a one-off to start.
	Controllers(hcpClusterID string) ResourceCRUD[resourcesapi.Controller]
}

type OperationCRUD interface {
	ResourceCRUD[resourcesapi.Operation]

	// ListActiveOperations returns an iterator that searches for asynchronous operation documents
	// with a non-terminal status in the "Resources" container under the given partition key. The
	// options argument can further limit the search to documents that match the provided values.
	//
	// Note that ListActiveOperations does not perform the search, but merely prepares an iterator
	// to do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) DBClientIterator[resourcesapi.Operation]
}

type operationCRUD struct {
	*nestedCosmosResourceCRUD[resourcesapi.Operation, GenericDocument[resourcesapi.Operation]]
}

func NewOperationCRUD(containerClient *azcosmos.ContainerClient, subscriptionID string) OperationCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(path.Join(parts...)))

	return &operationCRUD{
		nestedCosmosResourceCRUD: NewCosmosResourceCRUD[resourcesapi.Operation, GenericDocument[resourcesapi.Operation]](containerClient, parentResourceID, resourcesapi.OperationStatusResourceType),
	}
}

var _ OperationCRUD = &operationCRUD{}

func (d *operationCRUD) ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) DBClientIterator[resourcesapi.Operation] {
	var queryOptions azcosmos.QueryOptions

	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
		resourcesapi.OperationStatusResourceType.String(),
		armresourcesapi.ProvisioningStateSucceeded,
		armresourcesapi.ProvisioningStateFailed,
		armresourcesapi.ProvisioningStateCanceled)

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
	return newQueryResourcesIterator[resourcesapi.Operation, GenericDocument[resourcesapi.Operation]](pager)
}

type HCPClusterCRUD interface {
	ResourceCRUD[resourcesapi.HCPOpenShiftCluster]
	ControllerContainer
	ManagementClusterContentContainer

	ExternalAuth(hcpClusterID string) ExternalAuthsCRUD
	NodePools(hcpClusterID string) NodePoolsCRUD
}

func NewHCPClusterCRUD(containerClient *azcosmos.ContainerClient, subscriptionID, resourceGroupName string) HCPClusterCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	if len(resourceGroupName) > 0 {
		parts = append(parts,
			"resourceGroups",
			resourceGroupName)
	}
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(strings.ToLower(path.Join(parts...))))

	return &hcpClusterCRUD{
		nestedCosmosResourceCRUD: NewCosmosResourceCRUD[resourcesapi.HCPOpenShiftCluster, HCPCluster](containerClient, parentResourceID, resourcesapi.ClusterResourceType),
	}
}

type NodePoolsCRUD interface {
	ResourceCRUD[resourcesapi.HCPOpenShiftClusterNodePool]
	ControllerContainer
	ManagementClusterContentContainer
}

type ExternalAuthsCRUD interface {
	ResourceCRUD[resourcesapi.HCPOpenShiftClusterExternalAuth]
	ControllerContainer
}

type hcpClusterCRUD struct {
	*nestedCosmosResourceCRUD[resourcesapi.HCPOpenShiftCluster, HCPCluster]
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuth(hcpClusterName string) ExternalAuthsCRUD {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &externalAuthCRUD{
		nestedCosmosResourceCRUD: NewCosmosResourceCRUD[resourcesapi.HCPOpenShiftClusterExternalAuth, ExternalAuth](
			h.containerClient,
			parentResourceID,
			resourcesapi.ExternalAuthResourceType,
		),
	}
}

func (h *hcpClusterCRUD) NodePools(hcpClusterName string) NodePoolsCRUD {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &nodePoolsCRUD{
		nestedCosmosResourceCRUD: NewCosmosResourceCRUD[resourcesapi.HCPOpenShiftClusterNodePool, NodePool](
			h.containerClient,
			parentResourceID,
			resourcesapi.NodePoolResourceType),
	}
}

func (h *hcpClusterCRUD) Controllers(hcpClusterName string) ResourceCRUD[resourcesapi.Controller] {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return NewControllerCRUD(h.containerClient, parentResourceID, resourcesapi.ClusterControllerResourceType)
}

func (h *hcpClusterCRUD) ManagementClusterContents(hcpClusterName string) ManagementClusterContentCRUD {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return NewCosmosResourceCRUD[resourcesapi.ManagementClusterContent, GenericDocument[resourcesapi.ManagementClusterContent]](
		h.containerClient,
		parentResourceID,
		resourcesapi.ClusterScopedManagementClusterContentResourceType,
	)
}

type externalAuthCRUD struct {
	*nestedCosmosResourceCRUD[resourcesapi.HCPOpenShiftClusterExternalAuth, ExternalAuth]
}

func (h *externalAuthCRUD) Controllers(externalAuthName string) ResourceCRUD[resourcesapi.Controller] {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			externalAuthName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, resourcesapi.ExternalAuthControllerResourceType)
}

type nodePoolsCRUD struct {
	*nestedCosmosResourceCRUD[resourcesapi.HCPOpenShiftClusterNodePool, NodePool]
}

func (h *nodePoolsCRUD) Controllers(nodePoolName string) ResourceCRUD[resourcesapi.Controller] {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, resourcesapi.NodePoolControllerResourceType)
}

func (h *nodePoolsCRUD) ManagementClusterContents(nodePoolName string) ManagementClusterContentCRUD {
	parentResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewCosmosResourceCRUD[resourcesapi.ManagementClusterContent, GenericDocument[resourcesapi.ManagementClusterContent]](
		h.containerClient,
		parentResourceID,
		resourcesapi.NodePoolScopedManagementClusterContentResourceType,
	)
}

func NewControllerCRUD(
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) ResourceCRUD[resourcesapi.Controller] {

	return NewCosmosResourceCRUD[resourcesapi.Controller, GenericDocument[resourcesapi.Controller]](containerClient, parentResourceID, resourceType)
}
