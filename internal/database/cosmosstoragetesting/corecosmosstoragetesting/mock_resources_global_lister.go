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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// mockResourcesGlobalListers implements corecosmosstorage.ResourcesGlobalListers for the mock client.
type mockResourcesGlobalListers struct {
	client *MockResourcesDBClient
}

var _ corecosmosstorage.ResourcesGlobalListers = &mockResourcesGlobalListers{}

func (g *mockResourcesGlobalListers) Subscriptions() cosmosstorageutils.GlobalLister[arm.Subscription] {
	return &mockSubscriptionGlobalLister{client: g.client}
}

func (g *mockResourcesGlobalListers) Clusters() cosmosstorageutils.GlobalLister[api.HCPOpenShiftCluster] {
	return &MockGlobalLister[api.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[api.HCPOpenShiftCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ClusterResourceType},
	}
}

func (g *mockResourcesGlobalListers) NodePools() cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterNodePool] {
	return &MockGlobalLister[api.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterNodePool]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.NodePoolResourceType},
	}
}

func (g *mockResourcesGlobalListers) ExternalAuths() cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterExternalAuth] {
	return &MockGlobalLister[api.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterExternalAuth]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ExternalAuthResourceType},
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderClusters() cosmosstorageutils.GlobalLister[api.ServiceProviderCluster] {
	return &MockGlobalLister[api.ServiceProviderCluster, cosmosstorageutils.GenericDocument[api.ServiceProviderCluster]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ServiceProviderClusterResourceType},
	}
}

func (g *mockResourcesGlobalListers) ServiceProviderNodePools() cosmosstorageutils.GlobalLister[api.ServiceProviderNodePool] {
	return &MockGlobalLister[api.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[api.ServiceProviderNodePool]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.ServiceProviderNodePoolResourceType},
	}
}

func (g *mockResourcesGlobalListers) Controllers() cosmosstorageutils.GlobalLister[api.Controller] {
	return &MockGlobalLister[api.Controller, cosmosstorageutils.GenericDocument[api.Controller]]{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			api.ClusterControllerResourceType,
			api.NodePoolControllerResourceType,
			api.ExternalAuthControllerResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) ManagementClusterContents() cosmosstorageutils.GlobalLister[api.ManagementClusterContent] {
	return &MockGlobalLister[api.ManagementClusterContent, cosmosstorageutils.GenericDocument[api.ManagementClusterContent]]{
		client: g.client,
		resourceTypes: []azcorearm.ResourceType{
			api.ClusterScopedManagementClusterContentResourceType,
			api.NodePoolScopedManagementClusterContentResourceType,
		},
	}
}

func (g *mockResourcesGlobalListers) SystemAdminCredentialRequests() cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRequest] {
	return &MockGlobalLister[api.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRequest]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.SystemAdminCredentialRequestResourceType},
	}
}

func (g *mockResourcesGlobalListers) SystemAdminCredentialRevocations() cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRevocation] {
	return &MockGlobalLister[api.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRevocation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.SystemAdminCredentialRevocationResourceType},
	}
}

func (g *mockResourcesGlobalListers) Operations() cosmosstorageutils.GlobalLister[api.Operation] {
	return &MockGlobalLister[api.Operation, cosmosstorageutils.GenericDocument[api.Operation]]{
		client:        g.client,
		resourceTypes: []azcorearm.ResourceType{api.OperationStatusResourceType},
	}
}

func (g *mockResourcesGlobalListers) ActiveOperations() cosmosstorageutils.GlobalLister[api.Operation] {
	return &mockActiveOperationsGlobalLister{client: g.client}
}

// mockSubscriptionGlobalLister lists all subscriptions across all partitions.
type mockSubscriptionGlobalLister struct {
	client *MockResourcesDBClient
}

func (l *mockSubscriptionGlobalLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[arm.Subscription], error) {
	documents := l.client.ListDocuments(&azcorearm.SubscriptionResourceType, "")

	var ids []string
	var items []*arm.Subscription

	for _, data := range documents {
		var cosmosObj cosmosstorageutils.GenericDocument[arm.Subscription]
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

func (l *mockActiveOperationsGlobalLister) List(ctx context.Context, options *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[api.Operation], error) {
	allDocs := l.client.GetAllDocuments()

	var ids []string
	var items []*api.Operation

	for _, data := range allDocs {
		var typedDoc cosmosstorageutils.TypedDocument
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

		var cosmosObj cosmosstorageutils.GenericDocument[api.Operation]
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
