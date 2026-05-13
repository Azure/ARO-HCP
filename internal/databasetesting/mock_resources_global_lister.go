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

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// mockResourcesGlobalListers implements database.ResourcesGlobalListers for the mock client.
type mockResourcesGlobalListers struct {
	client *MockResourcesDBClient
}

var _ database.ResourcesGlobalListers = &mockResourcesGlobalListers{}

func (g *mockResourcesGlobalListers) Subscriptions() database.GlobalLister[armresourcesapi.Subscription] {
	return &mockSubscriptionGlobalLister{client: g.client}
}

func (g *mockResourcesGlobalListers) Clusters() database.GlobalLister[resourcesapi.HCPOpenShiftCluster] {
	return &mockTypedGlobalLister[resourcesapi.HCPOpenShiftCluster, database.HCPCluster]{
		client:       g.client,
		resourceType: resourcesapi.ClusterResourceType,
	}
}

func (g *mockResourcesGlobalListers) NodePools() database.GlobalLister[resourcesapi.HCPOpenShiftClusterNodePool] {
	return &mockTypedGlobalLister[resourcesapi.HCPOpenShiftClusterNodePool, database.NodePool]{
		client:       g.client,
		resourceType: resourcesapi.NodePoolResourceType,
	}
}

func (g *mockResourcesGlobalListers) ExternalAuths() database.GlobalLister[resourcesapi.HCPOpenShiftClusterExternalAuth] {
	return &mockTypedGlobalLister[resourcesapi.HCPOpenShiftClusterExternalAuth, database.ExternalAuth]{
		client:       g.client,
		resourceType: resourcesapi.ExternalAuthResourceType,
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderClusters() database.GlobalLister[resourcesapi.ServiceProviderCluster] {
	return &mockTypedGlobalLister[resourcesapi.ServiceProviderCluster, database.GenericDocument[resourcesapi.ServiceProviderCluster]]{
		client:       g.client,
		resourceType: resourcesapi.ServiceProviderClusterResourceType,
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderNodePools() database.GlobalLister[resourcesapi.ServiceProviderNodePool] {
	return &mockTypedGlobalLister[resourcesapi.ServiceProviderNodePool, database.GenericDocument[resourcesapi.ServiceProviderNodePool]]{
		client:       g.client,
		resourceType: resourcesapi.ServiceProviderNodePoolResourceType,
	}
}

func (g *mockResourcesGlobalListers) Controllers() database.GlobalLister[resourcesapi.Controller] {
	return &mockControllerGlobalLister{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			resourcesapi.ClusterControllerResourceType,
			resourcesapi.NodePoolControllerResourceType,
			resourcesapi.ExternalAuthControllerResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) ManagementClusterContents() database.GlobalLister[resourcesapi.ManagementClusterContent] {
	return &mockManagementClusterContentGlobalLister{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			resourcesapi.ClusterScopedManagementClusterContentResourceType,
			resourcesapi.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) Operations() database.GlobalLister[resourcesapi.Operation] {
	return &mockTypedGlobalLister[resourcesapi.Operation, database.GenericDocument[resourcesapi.Operation]]{
		client:       g.client,
		resourceType: resourcesapi.OperationStatusResourceType,
	}
}

func (g *mockResourcesGlobalListers) ActiveOperations() database.GlobalLister[resourcesapi.Operation] {
	return &mockActiveOperationsGlobalLister{client: g.client}
}

// mockSubscriptionGlobalLister lists all subscriptions across all partitions.
type mockSubscriptionGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockSubscriptionGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[armresourcesapi.Subscription], error) {
	documents := l.client.ListDocuments(&azcorearm.SubscriptionResourceType, "")

	var ids []string
	var items []*armresourcesapi.Subscription

	for _, data := range documents {
		var cosmosObj database.GenericDocument[armresourcesapi.Subscription]
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

// mockTypedGlobalLister is a generic mock global lister that lists all resources
// of a given type across all partitions.
type mockTypedGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	client       mockDocumentStore
	resourceType azcorearm.ResourceType
}

func (l *mockTypedGlobalLister[InternalAPIType, CosmosAPIType]) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[InternalAPIType], error) {
	documents := l.client.ListDocuments(&l.resourceType, "")

	var ids []string
	var items []*InternalAPIType

	for _, data := range documents {
		var cosmosObj CosmosAPIType
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		internalObj, err := database.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
		if err != nil {
			continue
		}

		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

// mockActiveOperationsGlobalLister lists operations with non-terminal status
// across all partitions.
type mockActiveOperationsGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockActiveOperationsGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[resourcesapi.Operation], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*resourcesapi.Operation

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		if !strings.EqualFold(typedDoc.ResourceType, resourcesapi.OperationStatusResourceType.String()) {
			continue
		}

		var cosmosObj database.GenericDocument[resourcesapi.Operation]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		// Filter out terminal states.
		status := cosmosObj.Content.Status
		if status == armresourcesapi.ProvisioningStateSucceeded ||
			status == armresourcesapi.ProvisioningStateFailed ||
			status == armresourcesapi.ProvisioningStateCanceled {
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

// mockControllerGlobalLister lists controllers across all partitions.
type mockControllerGlobalLister struct {
	client        *MockResourcesDBClient
	resourceTypes []azcorearm.ResourceType
}

func (l *mockControllerGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[resourcesapi.Controller], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*resourcesapi.Controller

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		// check if any of the resourceTypes match the one from the doc
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

		var cosmosObj database.GenericDocument[resourcesapi.Controller]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
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

// mockManagementClusterContentGlobalLister lists management cluster content for cluster-scoped and node-pool-scoped documents.
type mockManagementClusterContentGlobalLister struct {
	client        *MockResourcesDBClient
	resourceTypes []azcorearm.ResourceType
}

func (l *mockManagementClusterContentGlobalLister) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[resourcesapi.ManagementClusterContent], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*resourcesapi.ManagementClusterContent

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

		var cosmosObj database.GenericDocument[resourcesapi.ManagementClusterContent]
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
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
