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

// mockGlobalListers implements database.GlobalListers for the mock client.
type mockGlobalListers struct {
	client *MockDBClient
}

var _ database.GlobalListers = &mockGlobalListers{}

func (g *mockGlobalListers) Subscriptions() database.GlobalLister[arm.Subscription] {
	return &mockSubscriptionGlobalLister{client: g.client}
}

func (g *mockGlobalListers) Clusters() database.GlobalLister[api.HCPOpenShiftCluster] {
	return &mockTypedGlobalLister[api.HCPOpenShiftCluster, database.HCPCluster]{
		client:       g.client,
		resourceType: api.ClusterResourceType,
	}
}

func (g *mockGlobalListers) NodePools() database.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &mockTypedGlobalLister[api.HCPOpenShiftClusterNodePool, database.NodePool]{
		client:       g.client,
		resourceType: api.NodePoolResourceType,
	}
}

func (g *mockGlobalListers) ExternalAuths() database.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &mockTypedGlobalLister[api.HCPOpenShiftClusterExternalAuth, database.ExternalAuth]{
		client:       g.client,
		resourceType: api.ExternalAuthResourceType,
	}
}

func (g *mockGlobalListers) ServiceProviderClusters() database.GlobalLister[api.ServiceProviderCluster] {
	return &mockTypedGlobalLister[api.ServiceProviderCluster, database.GenericDocument[api.ServiceProviderCluster]]{
		client:       g.client,
		resourceType: api.ServiceProviderClusterResourceType,
	}
}

func (g *mockGlobalListers) Operations() database.GlobalLister[api.Operation] {
	return &mockTypedGlobalLister[api.Operation, database.GenericDocument[api.Operation]]{
		client:       g.client,
		resourceType: api.OperationStatusResourceType,
	}
}

func (g *mockGlobalListers) ActiveOperations() database.GlobalLister[api.Operation] {
	return &mockActiveOperationsGlobalLister{client: g.client}
}

// mockSubscriptionGlobalLister lists all subscriptions across all partitions.
type mockSubscriptionGlobalLister struct {
	client *MockDBClient
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

// mockTypedGlobalLister is a generic mock global lister that lists all resources
// of a given type across all partitions.
type mockTypedGlobalLister[InternalAPIType, CosmosAPIType any] struct {
	client       *MockDBClient
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
	client *MockDBClient
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
