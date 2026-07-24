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

package databasetesting

import (
	"context"
	"encoding/json"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// mockResourcesGlobalListers implements database.ResourcesGlobalListers for the mock client.
type mockResourcesGlobalListers struct {
	client *MockResourcesDBClient
}

var _ database.ResourcesGlobalListers = &mockResourcesGlobalListers{}

func (g *mockResourcesGlobalListers) Subscriptions() database.GlobalLister[arm.Subscription] {
	return &mockSubscriptionGlobalLister{client: g.client}
}

func (g *mockResourcesGlobalListers) Clusters() database.GlobalLister[api.HCPOpenShiftCluster] {
	return &mockGlobalLister[api.HCPOpenShiftCluster, database.GenericDocument[api.HCPOpenShiftCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ClusterResourceType},
	}
}

func (g *mockResourcesGlobalListers) NodePools() database.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &mockGlobalLister[api.HCPOpenShiftClusterNodePool, database.GenericDocument[api.HCPOpenShiftClusterNodePool]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.NodePoolResourceType},
	}
}

func (g *mockResourcesGlobalListers) ExternalAuths() database.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &mockGlobalLister[api.HCPOpenShiftClusterExternalAuth, database.GenericDocument[api.HCPOpenShiftClusterExternalAuth]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ExternalAuthResourceType},
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &mockGlobalLister[api.ServiceProviderCluster, database.GenericDocument[api.ServiceProviderCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ServiceProviderClusterResourceType},
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderNodePools() database.GlobalLister[api.ServiceProviderNodePool] {
	return &mockGlobalLister[api.ServiceProviderNodePool, database.GenericDocument[api.ServiceProviderNodePool]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ServiceProviderNodePoolResourceType},
	}
}

func (g *mockResourcesGlobalListers) Controllers() database.GlobalLister[api.Controller] {
	return &mockGlobalLister[api.Controller, database.GenericDocument[api.Controller]]{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			api.ClusterControllerResourceType,
			api.NodePoolControllerResourceType,
			api.ExternalAuthControllerResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) ManagementClusterContents() database.GlobalLister[api.ManagementClusterContent] {
	return &mockGlobalLister[api.ManagementClusterContent, database.GenericDocument[api.ManagementClusterContent]]{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			api.ClusterScopedManagementClusterContentResourceType,
			api.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) SystemAdminCredentialRequests() database.GlobalLister[api.SystemAdminCredentialRequest] {
	return &mockGlobalLister[api.SystemAdminCredentialRequest, database.GenericDocument[api.SystemAdminCredentialRequest]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.SystemAdminCredentialRequestResourceType},
	}
}

func (g *mockResourcesGlobalListers) SystemAdminCredentialRevocations() database.GlobalLister[api.SystemAdminCredentialRevocation] {
	return &mockGlobalLister[api.SystemAdminCredentialRevocation, database.GenericDocument[api.SystemAdminCredentialRevocation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.SystemAdminCredentialRevocationResourceType},
	}
}

func (g *mockResourcesGlobalListers) Operations() database.GlobalLister[api.Operation] {
	return &mockGlobalLister[api.Operation, database.GenericDocument[api.Operation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.OperationStatusResourceType},
	}
}

func (g *mockResourcesGlobalListers) ActiveOperations() database.GlobalLister[api.Operation] {
	return &mockActiveOperationsGlobalLister{client: g.client}
}

// mockSubscriptionGlobalLister lists all subscriptions across all partitions.
type mockSubscriptionGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockSubscriptionGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[arm.Subscription], error) {
	documents := l.client.ListDocuments(&azcorearm.SubscriptionResourceType, "")

	var ids []string
	var items []*arm.Subscription

	for _, data := range documents {
		var cosmosObj database.GenericDocument[arm.Subscription]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		internalObj, err := database.CosmosGenericToInternal(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, cosmosObj.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

// mockActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type mockActiveOperationsGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockActiveOperationsGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.Operation], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*api.Operation

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		if !strings.EqualFold(typedDoc.ResourceType, api.OperationStatusResourceType.String()) {
			continue
		}

		if typedDoc.ResourceID == nil {
			continue
		}

		if typedDoc.DeletionTimestamp != nil {
			continue
		}

		var cosmosObj database.GenericDocument[api.Operation]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		// Filter out terminal states.
		status := cosmosObj.Content.Status
		if status == arm.ProvisioningStateSucceeded ||
			status == arm.ProvisioningStateFailed ||
			status == arm.ProvisioningStateCanceled {
			continue
		}

		internalObj, err := database.CosmosGenericToInternal(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

// mockGlobalLister mirrors the production cosmosGlobalLister: it walks the
// document store and emits every document whose resourceType matches one of
// resourceTypes. Documents without a resourceID are dropped to mirror the
// production query's LENGTH(c.resourceID) > 0 filter.
type mockGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	client        mockDocumentStore
	resourceTypes []azcorearm.ResourceType
}

func (l *mockGlobalLister[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[InternalAPIType], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*InternalAPIType

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
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

		internalObj, err := database.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

// mockBillingGlobalLister lists all billing documents across all partitions.
type mockBillingGlobalLister struct {
	store *mockBillingStore
}

func (l *mockBillingGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[database.BillingDocument], error) {
	l.store.mu.RLock()
	defer l.store.mu.RUnlock()

	var ids []string
	var items []*database.BillingDocument

	for id, doc := range l.store.docs {
		ids = append(ids, id)
		items = append(items, doc)
	}

	return newMockIterator(ids, items), nil
}
