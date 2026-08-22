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

package corecosmosstoragetesting

import (
	"context"
	"encoding/json"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// mockResourcesGlobalListers implements corecosmosstorage.ResourcesGlobalListers for the mock client.
type mockResourcesGlobalListers struct {
	client *MockResourcesDBClient
}

var _ corecosmosstorage.ResourcesGlobalListers = &mockResourcesGlobalListers{}

func (g *mockResourcesGlobalListers) Subscriptions() cosmosstorageutils.GlobalLister[coreapi.Subscription] {
	return &mockSubscriptionGlobalLister{client: g.client}
}

func (g *mockResourcesGlobalListers) Clusters() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftCluster] {
	return &MockGlobalLister[coreapi.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.ClusterResourceType},
	}
}

func (g *mockResourcesGlobalListers) NodePools() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftClusterNodePool] {
	return &MockGlobalLister[coreapi.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterNodePool]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.NodePoolResourceType},
	}
}

func (g *mockResourcesGlobalListers) ExternalAuths() cosmosstorageutils.GlobalLister[coreapi.HCPOpenShiftClusterExternalAuth] {
	return &MockGlobalLister[coreapi.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterExternalAuth]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.ExternalAuthResourceType},
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderClusters() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderCluster] {
	return &MockGlobalLister[coreapi.ServiceProviderCluster, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.ServiceProviderClusterResourceType},
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderNodePools() cosmosstorageutils.GlobalLister[coreapi.ServiceProviderNodePool] {
	return &MockGlobalLister[coreapi.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderNodePool]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.ServiceProviderNodePoolResourceType},
	}
}

func (g *mockResourcesGlobalListers) Controllers() cosmosstorageutils.GlobalLister[coreapi.Controller] {
	return &MockGlobalLister[coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]]{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			coreapi.ClusterControllerResourceType,
			coreapi.NodePoolControllerResourceType,
			coreapi.ExternalAuthControllerResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) ManagementClusterContents() cosmosstorageutils.GlobalLister[coreapi.ManagementClusterContent] {
	return &MockGlobalLister[coreapi.ManagementClusterContent, cosmosstorageutils.GenericDocument[coreapi.ManagementClusterContent]]{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			coreapi.ClusterScopedManagementClusterContentResourceType,
			coreapi.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) SystemAdminCredentialRequests() cosmosstorageutils.GlobalLister[coreapi.SystemAdminCredentialRequest] {
	return &MockGlobalLister[coreapi.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRequest]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.SystemAdminCredentialRequestResourceType},
	}
}

func (g *mockResourcesGlobalListers) SystemAdminCredentialRevocations() cosmosstorageutils.GlobalLister[coreapi.SystemAdminCredentialRevocation] {
	return &MockGlobalLister[coreapi.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRevocation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.SystemAdminCredentialRevocationResourceType},
	}
}

func (g *mockResourcesGlobalListers) DNSReservations() cosmosstorageutils.GlobalLister[coreapi.DNSReservation] {
	return &MockGlobalLister[coreapi.DNSReservation, cosmosstorageutils.GenericDocument[coreapi.DNSReservation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.DNSReservationResourceType},
	}
}

func (g *mockResourcesGlobalListers) Operations() cosmosstorageutils.GlobalLister[coreapi.Operation] {
	return &MockGlobalLister[coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{coreapi.OperationStatusResourceType},
	}
}

func (g *mockResourcesGlobalListers) ActiveOperations() cosmosstorageutils.GlobalLister[coreapi.Operation] {
	return &mockActiveOperationsGlobalLister{client: g.client}
}

// mockSubscriptionGlobalLister lists all subscriptions across all partitions.
type mockSubscriptionGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockSubscriptionGlobalLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Subscription], error) {
	documents := l.client.ListDocuments(&azcorearm.SubscriptionResourceType, "")

	var ids []string
	var items []*coreapi.Subscription

	for _, data := range documents {
		var cosmosObj cosmosstorageutils.GenericDocument[coreapi.Subscription]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		internalObj, err := cosmosstorageutils.CosmosGenericToInternal(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, cosmosObj.ID)
		items = append(items, internalObj)
	}

	return NewMockIterator(ids, items), nil
}

// mockActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type mockActiveOperationsGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockActiveOperationsGlobalLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Operation], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*coreapi.Operation

	for _, data := range allDocs {
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		if !strings.EqualFold(typedDoc.ResourceType, coreapi.OperationStatusResourceType.String()) {
			continue
		}

		if typedDoc.ResourceID == nil {
			continue
		}

		if typedDoc.DeletionTimestamp != nil {
			continue
		}

		var cosmosObj cosmosstorageutils.GenericDocument[coreapi.Operation]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		// Filter out terminal states.
		status := cosmosObj.Content.Status
		if status == coreapi.ProvisioningStateSucceeded ||
			status == coreapi.ProvisioningStateFailed ||
			status == coreapi.ProvisioningStateCanceled {
			continue
		}

		internalObj, err := cosmosstorageutils.CosmosGenericToInternal(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return NewMockIterator(ids, items), nil
}

// MockGlobalLister mirrors the production cosmosGlobalLister: it walks the
// document store and emits every document whose resourceType matches one of
// resourceTypes. Documents without a resourceID are dropped to mirror the
// production query's LENGTH(c.resourceID) > 0 filter.
type MockGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	client        MockDocumentStore
	resourceTypes []azcorearm.ResourceType
}

func (l *MockGlobalLister[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[InternalAPIType], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*InternalAPIType

	for _, data := range allDocs {
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		resourceTypeMatches := false
		for _, resourceType := range l.resourceTypes {
			if strings.EqualFold(typedDoc.ResourceType, resourceType.String()) {
				resourceTypeMatches = true
				break
			}
		}
		if !resourceTypeMatches {
			continue
		}

		if typedDoc.ResourceID == nil {
			continue
		}

		if typedDoc.DeletionTimestamp != nil {
			continue
		}

		var cosmosObj CosmosAPIType
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		internalObj, err := cosmosstorageutils.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return NewMockIterator(ids, items), nil
}

// NewMockGlobalLister builds a MockGlobalLister over the given document store,
// matching documents whose resourceType is any of resourceTypes.
func NewMockGlobalLister[InternalAPIType, CosmosAPIType any](client MockDocumentStore, resourceTypes []azcorearm.ResourceType) *MockGlobalLister[InternalAPIType, CosmosAPIType] {
	return &MockGlobalLister[InternalAPIType, CosmosAPIType]{client: client, resourceTypes: resourceTypes}
}
