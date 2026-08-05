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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

type ControllerContainer interface {
	// TODO controllers are a concept that is at this scope and at lower scopes and sometimes you want to query all like it
	// TODO they look a lot like operations, though we can model them as a one-off to start.
	Controllers(hcpClusterID string) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller]
}

type OperationCRUD interface {
	cosmosstorageutils.ResourceCRUD[api.Operation, *api.Operation]

	// ListActiveOperations returns an iterator that searches for asynchronous operation documents
	// with a non-terminal status in the "Resources" container under the given partition key. The
	// options argument can further limit the search to documents that match the provided values.
	//
	// Note that ListActiveOperations does not perform the search, but merely prepares an iterator
	// to do so. Hence the lack of a Context argument. The search is performed by calling Items() on
	// the iterator in a ranged for loop.
	ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) cosmosstorageutils.DBClientIterator[api.Operation]
}

type operationCRUD struct {
	*cosmosstorageutils.NestedCosmosResourceCRUD[api.Operation, *api.Operation, cosmosstorageutils.GenericDocument[api.Operation]]
}

func NewOperationCRUD(containerClient *azcosmos.ContainerClient, subscriptionID string) OperationCRUD {
	parts := []string{
		"/subscriptions",
		strings.ToLower(subscriptionID),
	}
	parentResourceID := api.Must(azcorearm.ParseResourceID(path.Join(parts...)))

	return &operationCRUD{
		NestedCosmosResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[api.Operation, *api.Operation, cosmosstorageutils.GenericDocument[api.Operation]](containerClient, parentResourceID, api.OperationStatusResourceType),
	}
}

var _ OperationCRUD = &operationCRUD{}

func (d *operationCRUD) ListActiveOperations(options *ResourcesDBClientListActiveOperationDocsOptions) cosmosstorageutils.DBClientIterator[api.Operation] {
	var queryOptions azcosmos.QueryOptions

	query := fmt.Sprintf(
		"SELECT * FROM c WHERE STRINGEQUALS(c.resourceType, %q, true) "+
			"AND LENGTH(c.resourceID) > 0 "+
			"AND (NOT IS_DEFINED(c.deletionTimestamp)) "+
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

	pager := d.ContainerClient.NewQueryItemsPager(query, cosmosstorageutils.NewPartitionKey(d.ParentResourceID.SubscriptionID), &queryOptions)
	return cosmosstorageutils.NewQueryResourcesIterator[api.Operation, cosmosstorageutils.GenericDocument[api.Operation]](pager)
}

type HCPClusterCRUD interface {
	cosmosstorageutils.ResourceCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster]
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
		parentResourceID = api.Must(api.ToResourceGroupResourceID(subscriptionID, resourceGroupName))
	} else {
		parentResourceID = api.Must(arm.ToSubscriptionResourceID(subscriptionID))
	}

	return &hcpClusterCRUD{
		NestedCosmosResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[api.HCPOpenShiftCluster]](containerClient, parentResourceID, api.ClusterResourceType),
	}
}

type NodePoolsCRUD interface {
	cosmosstorageutils.ResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool]
	ControllerContainer
	ManagementClusterContentContainer
}

type ExternalAuthsCRUD interface {
	cosmosstorageutils.ResourceCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth]
	ControllerContainer
}

type hcpClusterCRUD struct {
	*cosmosstorageutils.NestedCosmosResourceCRUD[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[api.HCPOpenShiftCluster]]
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuth(hcpClusterName string) ExternalAuthsCRUD {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			"providers",
			h.ResourceType.Namespace,
			h.ResourceType.Type,
			hcpClusterName)))

	return &externalAuthCRUD{
		NestedCosmosResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterExternalAuth]](
			h.ContainerClient,
			parentResourceID,
			api.ExternalAuthResourceType,
		),
	}
}

func (h *hcpClusterCRUD) NodePools(hcpClusterName string) NodePoolsCRUD {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			"providers",
			h.ResourceType.Namespace,
			h.ResourceType.Type,
			hcpClusterName)))

	return &nodePoolsCRUD{
		NestedCosmosResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterNodePool]](
			h.ContainerClient,
			parentResourceID,
			api.NodePoolResourceType),
	}
}

func (h *hcpClusterCRUD) SystemAdminCredentialRequests(hcpClusterName string) SystemAdminCredentialRequestsCRUD {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			"providers",
			h.ResourceType.Namespace,
			h.ResourceType.Type,
			hcpClusterName)))

	return &systemAdminCredentialRequestsCRUD{
		NestedCosmosResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRequest]](
			h.ContainerClient,
			clusterResourceID,
			api.SystemAdminCredentialRequestResourceType),
	}
}

func (h *hcpClusterCRUD) SystemAdminCredentialRevocations(hcpClusterName string) SystemAdminCredentialRevocationsCRUD {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			"providers",
			h.ResourceType.Namespace,
			h.ResourceType.Type,
			hcpClusterName)))

	return &systemAdminCredentialRevocationsCRUD{
		NestedCosmosResourceCRUD: cosmosstorageutils.NewCosmosResourceCRUD[api.SystemAdminCredentialRevocation, *api.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRevocation]](
			h.ContainerClient,
			clusterResourceID,
			api.SystemAdminCredentialRevocationResourceType),
	}
}

func (h *hcpClusterCRUD) Controllers(hcpClusterName string) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			"providers",
			h.ResourceType.Namespace,
			h.ResourceType.Type,
			hcpClusterName)))

	return NewControllerCRUD(h.ContainerClient, parentResourceID, api.ClusterControllerResourceType)
}

func (h *hcpClusterCRUD) ManagementClusterContents(hcpClusterName string) cosmosstorageutils.ResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			"providers",
			h.ResourceType.Namespace,
			h.ResourceType.Type,
			hcpClusterName)))

	return cosmosstorageutils.NewCosmosResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent, cosmosstorageutils.GenericDocument[api.ManagementClusterContent]](
		h.ContainerClient,
		parentResourceID,
		api.ClusterScopedManagementClusterContentResourceType,
	)
}

type externalAuthCRUD struct {
	*cosmosstorageutils.NestedCosmosResourceCRUD[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterExternalAuth]]
}

func (h *externalAuthCRUD) Controllers(externalAuthName string) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			h.ResourceType.Types[len(h.ResourceType.Types)-1],
			externalAuthName,
		)))

	return NewControllerCRUD(h.ContainerClient, parentResourceID, api.ExternalAuthControllerResourceType)
}

type nodePoolsCRUD struct {
	*cosmosstorageutils.NestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterNodePool]]
}

func (h *nodePoolsCRUD) Controllers(nodePoolName string) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			h.ResourceType.Types[len(h.ResourceType.Types)-1],
			nodePoolName,
		)))

	return NewControllerCRUD(h.ContainerClient, parentResourceID, api.NodePoolControllerResourceType)
}

func (h *nodePoolsCRUD) ManagementClusterContents(nodePoolName string) cosmosstorageutils.ResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			h.ResourceType.Types[len(h.ResourceType.Types)-1],
			nodePoolName,
		)))

	return cosmosstorageutils.NewCosmosResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent, cosmosstorageutils.GenericDocument[api.ManagementClusterContent]](
		h.ContainerClient,
		parentResourceID,
		api.NodePoolScopedManagementClusterContentResourceType,
	)
}

type SystemAdminCredentialRequestsCRUD interface {
	cosmosstorageutils.ResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest]
	ControllerContainer
}

type systemAdminCredentialRequestsCRUD struct {
	*cosmosstorageutils.NestedCosmosResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRequest]]
}

func (h *systemAdminCredentialRequestsCRUD) Controllers(credentialRequestName string) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			h.ResourceType.Types[len(h.ResourceType.Types)-1],
			credentialRequestName,
		)))

	return NewControllerCRUD(h.ContainerClient, parentResourceID, api.SystemAdminCredentialRequestControllerResourceType)
}

var _ SystemAdminCredentialRequestsCRUD = &systemAdminCredentialRequestsCRUD{}

type SystemAdminCredentialRevocationsCRUD interface {
	cosmosstorageutils.ResourceCRUD[api.SystemAdminCredentialRevocation, *api.SystemAdminCredentialRevocation]
	ControllerContainer
}

type systemAdminCredentialRevocationsCRUD struct {
	*cosmosstorageutils.NestedCosmosResourceCRUD[api.SystemAdminCredentialRevocation, *api.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRevocation]]
}

func (h *systemAdminCredentialRevocationsCRUD) Controllers(revocationName string) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			h.ParentResourceID.String(),
			h.ResourceType.Types[len(h.ResourceType.Types)-1],
			revocationName,
		)))

	return NewControllerCRUD(h.ContainerClient, parentResourceID, api.SystemAdminCredentialRevocationControllerResourceType)
}

var _ SystemAdminCredentialRevocationsCRUD = &systemAdminCredentialRevocationsCRUD{}

func NewControllerCRUD(
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) cosmosstorageutils.ResourceCRUD[api.Controller, *api.Controller] {

	return cosmosstorageutils.NewCosmosResourceCRUD[api.Controller, *api.Controller, cosmosstorageutils.GenericDocument[api.Controller]](containerClient, parentResourceID, resourceType)
}
