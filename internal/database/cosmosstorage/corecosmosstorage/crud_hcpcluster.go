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

package corecosmosstorage

import (
	"fmt"
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

type ControllerContainer interface {
	// TODO controllers are a concept that is at this scope and at lower scopes and sometimes you want to query all like it
	// TODO they look a lot like operations, though we can model them as a one-off to start.
	Controllers(hcpClusterID string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller]
}

type OperationCRUD interface {
	cosmosstorageutils.ResourceCRUD[coreapi.Operation, *coreapi.Operation]

	// ListActiveOperations returns an iterator that searches for asynchronous operation documents
	// in the "Resources" container under the given partition key. By default only non-terminal
	// operations are returned; set IncludeTerminal to include Succeeded/Failed/Canceled.
	// The options argument can further limit the search to documents that match the provided values.
	//
	// Note that ListActiveOperations does not perform the search, but merely prepares an iterator
	// to do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) cosmosstorageutils.DBClientIterator[coreapi.Operation]
}

// operationCRUD embeds an instrumented ResourceCRUD for its CRUD operations and
// keeps the container client and parent resource ID separately so the
// ListActiveOperations accessor can build its own cross-partition query pager.
type operationCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.Operation, *coreapi.Operation]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
}

func NewOperationCRUD(containerClient *azcosmos.ContainerClient, subscriptionID string) OperationCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(path.Join(parts...)))

	return &operationCRUD{
		ResourceCRUD:     cosmosstorageutils.NewCosmosResourceCRUD[coreapi.Operation, *coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](containerClient, parentResourceID, coreapi.OperationStatusResourceType),
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
	}
}

var _ OperationCRUD = &operationCRUD{}

func (d *operationCRUD) ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) cosmosstorageutils.DBClientIterator[coreapi.Operation] {
	var queryOptions azcosmos.QueryOptions

	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND LENGTH(c.resourceID) > 0 "+
			"AND (NOT IS_DEFINED(c.deletionTimestamp))",
		coreapi.OperationStatusResourceType.String())

	if options == nil || !options.IncludeTerminal {
		query += fmt.Sprintf(
			" AND NOT ARRAYCONTAINS([%q, %q, %q], c.properties.status)",
			coreapi.ProvisioningStateSucceeded,
			coreapi.ProvisioningStateFailed,
			coreapi.ProvisioningStateCanceled)
	}

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

	pager := d.containerClient.NewQueryItemsPager(query, cosmosstorageutils.NewPartitionKey(d.parentResourceID.SubscriptionID), &queryOptions)
	return cosmosstorageutils.NewQueryResourcesIterator[coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](pager)
}

type HCPClusterCRUD interface {
	cosmosstorageutils.ResourceCRUD[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster]
	ControllerContainer
	ManagementClusterContentContainer

	ExternalAuth(hcpClusterID string) ExternalAuthsCRUD
	NodePools(hcpClusterID string) NodePoolsCRUD
	SystemAdminCredentialRequests(hcpClusterName string) SystemAdminCredentialRequestsCRUD
	SystemAdminCredentialRevocations(hcpClusterName string) SystemAdminCredentialRevocationsCRUD
}

func NewHCPClusterCRUD(containerClient *azcosmos.ContainerClient, subscriptionID, resourceGroupName string) HCPClusterCRUD {
	var parentResourceID *azcorearm.ResourceID
	if len(resourceGroupName) > 0 {
		parentResourceID = metadataapi.Must(coreapi.ToResourceGroupResourceID(subscriptionID, resourceGroupName))
	} else {
		parentResourceID = metadataapi.Must(coreapi.ToSubscriptionResourceID(subscriptionID))
	}

	return &hcpClusterCRUD{
		ResourceCRUD:     cosmosstorageutils.NewCosmosResourceCRUD[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftCluster]](containerClient, parentResourceID, coreapi.ClusterResourceType),
		containerClient:  containerClient,
		parentResourceID: parentResourceID,
		resourceType:     coreapi.ClusterResourceType,
	}
}

type NodePoolsCRUD interface {
	cosmosstorageutils.ResourceCRUD[coreapi.HCPOpenShiftClusterNodePool, *coreapi.HCPOpenShiftClusterNodePool]
	ControllerContainer
	ManagementClusterContentContainer
}

type ExternalAuthsCRUD interface {
	cosmosstorageutils.ResourceCRUD[coreapi.HCPOpenShiftClusterExternalAuth, *coreapi.HCPOpenShiftClusterExternalAuth]
	ControllerContainer
}

// hcpClusterCRUD embeds an instrumented ResourceCRUD for its CRUD operations and
// keeps the container client, parent resource ID and resource type separately so
// the nested-resource accessors (ExternalAuth, NodePools, SystemAdmin*, etc.) can
// build child CRUD scopes.
type hcpClusterCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuth(hcpClusterName string) ExternalAuthsCRUD {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &externalAuthCRUD{
		ResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[coreapi.HCPOpenShiftClusterExternalAuth, *coreapi.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterExternalAuth]](
			h.containerClient,
			parentResourceID,
			coreapi.ExternalAuthResourceType,
		),
		containerClient:  h.containerClient,
		parentResourceID: parentResourceID,
		resourceType:     coreapi.ExternalAuthResourceType,
	}
}

func (h *hcpClusterCRUD) NodePools(hcpClusterName string) NodePoolsCRUD {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &nodePoolsCRUD{
		ResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[coreapi.HCPOpenShiftClusterNodePool, *coreapi.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterNodePool]](
			h.containerClient,
			parentResourceID,
			coreapi.NodePoolResourceType),
		containerClient:  h.containerClient,
		parentResourceID: parentResourceID,
		resourceType:     coreapi.NodePoolResourceType,
	}
}

func (h *hcpClusterCRUD) SystemAdminCredentialRequests(hcpClusterName string) SystemAdminCredentialRequestsCRUD {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &systemAdminCredentialRequestsCRUD{
		ResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[coreapi.SystemAdminCredentialRequest, *coreapi.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRequest]](
			h.containerClient,
			clusterResourceID,
			coreapi.SystemAdminCredentialRequestResourceType),
		containerClient:  h.containerClient,
		parentResourceID: clusterResourceID,
		resourceType:     coreapi.SystemAdminCredentialRequestResourceType,
	}
}

func (h *hcpClusterCRUD) SystemAdminCredentialRevocations(hcpClusterName string) SystemAdminCredentialRevocationsCRUD {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return &systemAdminCredentialRevocationsCRUD{
		ResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[coreapi.SystemAdminCredentialRevocation, *coreapi.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRevocation]](
			h.containerClient,
			clusterResourceID,
			coreapi.SystemAdminCredentialRevocationResourceType),
		containerClient:  h.containerClient,
		parentResourceID: clusterResourceID,
		resourceType:     coreapi.SystemAdminCredentialRevocationResourceType,
	}
}

func (h *hcpClusterCRUD) Controllers(hcpClusterName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return NewControllerCRUD(h.containerClient, parentResourceID, coreapi.ClusterControllerResourceType)
}

func (h *hcpClusterCRUD) ManagementClusterContents(hcpClusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			"providers",
			h.resourceType.Namespace,
			h.resourceType.Type,
			hcpClusterName)))

	return cosmosstorageutils.NewCosmosResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent, cosmosstorageutils.GenericDocument[coreapi.ManagementClusterContent]](
		h.containerClient,
		parentResourceID,
		coreapi.ClusterScopedManagementClusterContentResourceType,
	)
}

// externalAuthCRUD embeds an instrumented ResourceCRUD and keeps the container
// client, parent resource ID and resource type separately so the Controllers
// accessor can build its child CRUD scope.
type externalAuthCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.HCPOpenShiftClusterExternalAuth, *coreapi.HCPOpenShiftClusterExternalAuth]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

func (h *externalAuthCRUD) Controllers(externalAuthName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			externalAuthName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, coreapi.ExternalAuthControllerResourceType)
}

// nodePoolsCRUD embeds an instrumented ResourceCRUD and keeps the container
// client, parent resource ID and resource type separately so the Controllers and
// ManagementClusterContents accessors can build their child CRUD scopes.
type nodePoolsCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.HCPOpenShiftClusterNodePool, *coreapi.HCPOpenShiftClusterNodePool]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

func (h *nodePoolsCRUD) Controllers(nodePoolName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, coreapi.NodePoolControllerResourceType)
}

func (h *nodePoolsCRUD) ManagementClusterContents(nodePoolName string) cosmosstorageutils.ResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			nodePoolName,
		)))

	return cosmosstorageutils.NewCosmosResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent, cosmosstorageutils.GenericDocument[coreapi.ManagementClusterContent]](
		h.containerClient,
		parentResourceID,
		coreapi.NodePoolScopedManagementClusterContentResourceType,
	)
}

type SystemAdminCredentialRequestsCRUD interface {
	cosmosstorageutils.ResourceCRUD[coreapi.SystemAdminCredentialRequest, *coreapi.SystemAdminCredentialRequest]
	ControllerContainer
}

// systemAdminCredentialRequestsCRUD embeds an instrumented ResourceCRUD and keeps
// the container client, parent resource ID and resource type separately so the
// Controllers accessor can build its child CRUD scope.
type systemAdminCredentialRequestsCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.SystemAdminCredentialRequest, *coreapi.SystemAdminCredentialRequest]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

func (h *systemAdminCredentialRequestsCRUD) Controllers(credentialRequestName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			credentialRequestName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, coreapi.SystemAdminCredentialRequestControllerResourceType)
}

var _ SystemAdminCredentialRequestsCRUD = &systemAdminCredentialRequestsCRUD{}

type SystemAdminCredentialRevocationsCRUD interface {
	cosmosstorageutils.ResourceCRUD[coreapi.SystemAdminCredentialRevocation, *coreapi.SystemAdminCredentialRevocation]
	ControllerContainer
}

// systemAdminCredentialRevocationsCRUD embeds an instrumented ResourceCRUD and
// keeps the container client, parent resource ID and resource type separately so
// the Controllers accessor can build its child CRUD scope.
type systemAdminCredentialRevocationsCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.SystemAdminCredentialRevocation, *coreapi.SystemAdminCredentialRevocation]
	containerClient  *azcosmos.ContainerClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

func (h *systemAdminCredentialRevocationsCRUD) Controllers(revocationName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			h.parentResourceID.String(),
			h.resourceType.Types[len(h.resourceType.Types)-1],
			revocationName,
		)))

	return NewControllerCRUD(h.containerClient, parentResourceID, coreapi.SystemAdminCredentialRevocationControllerResourceType)
}

var _ SystemAdminCredentialRevocationsCRUD = &systemAdminCredentialRevocationsCRUD{}

func NewControllerCRUD(
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {

	return cosmosstorageutils.NewCosmosResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](containerClient, parentResourceID, resourceType)
}
