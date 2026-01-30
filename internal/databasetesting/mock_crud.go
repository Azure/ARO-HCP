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

package databasetesting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// mockResourceCRUD is a generic mock implementation of database.ResourceCRUD.
type mockResourceCRUD[InternalAPIType, CosmosAPIType any] struct {
	client           *MockDBClient
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
}

func newMockResourceCRUD[InternalAPIType, CosmosAPIType any](
	client *MockDBClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) *mockResourceCRUD[InternalAPIType, CosmosAPIType] {

	return &mockResourceCRUD[InternalAPIType, CosmosAPIType]{
		client:           client,
		parentResourceID: parentResourceID,
		resourceType:     resourceType,
	}
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) makeResourceIDPath(resourceID string) (*azcorearm.ResourceID, error) {
	if len(m.parentResourceID.SubscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}
	parts := []string{m.parentResourceID.String()}

	if !strings.EqualFold(m.parentResourceID.ResourceType.Namespace, api.ProviderNamespace) {
		if len(resourceID) == 0 {
			resourcePathString := path.Join(parts...)
			return azcorearm.ParseResourceID(resourcePathString)
		}

		parts = append(parts,
			"providers",
			m.resourceType.Namespace,
		)
	} else {
		if len(m.parentResourceID.ResourceGroupName) == 0 {
			return nil, fmt.Errorf("resourceGroup is required")
		}
	}
	parts = append(parts, m.resourceType.Types[len(m.resourceType.Types)-1])

	if len(resourceID) > 0 {
		parts = append(parts, resourceID)
	}

	resourcePathString := path.Join(parts...)
	return azcorearm.ParseResourceID(resourcePathString)
}

func NewNotFoundError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "404 Not Found",
		StatusCode: http.StatusNotFound,
	}
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}

	data, ok := m.client.GetDocument(cosmosID)
	if !ok {
		return nil, NewNotFoundError()
	}

	var cosmosObj CosmosAPIType
	if err := json.Unmarshal(data, &cosmosObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	return database.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) Get(ctx context.Context, resourceID string) (*InternalAPIType, error) {
	completeResourceID, err := m.makeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	cosmosID, err := api.ResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return nil, err
	}

	return m.GetByID(ctx, cosmosID)
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[InternalAPIType], error) {
	prefix, err := m.makeResourceIDPath("")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path: %w", err)
	}

	documents := m.client.ListDocuments(&m.resourceType, prefix.String()+"/")

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

		// Get the ID from the typed document
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	cosmosObj, err := database.InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	// Get cosmos ID from the object
	cosmosPersistable, ok := any(newObj).(api.CosmosPersistable)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement CosmosPersistable", newObj)
	}

	cosmosData := cosmosPersistable.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	// Check for existing
	if _, exists := m.client.GetDocument(cosmosID); exists {
		return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
	}

	m.client.StoreDocument(cosmosID, data)

	// Read back the stored object
	return m.GetByID(ctx, cosmosID)
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) Replace(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	cosmosObj, err := database.InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	// Get cosmos ID from the object
	cosmosPersistable, ok := any(newObj).(api.CosmosPersistable)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement CosmosPersistable", newObj)
	}

	cosmosData := cosmosPersistable.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	// Check that document exists
	if _, exists := m.client.GetDocument(cosmosID); !exists {
		return nil, NewNotFoundError()
	}

	m.client.StoreDocument(cosmosID, data)

	// Read back the stored object
	return m.GetByID(ctx, cosmosID)
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) Delete(ctx context.Context, resourceID string) error {
	curr, err := m.Get(ctx, resourceID)
	if err != nil {
		return err
	}

	cosmosUID := any(curr).(api.CosmosPersistable).GetCosmosData().GetCosmosUID()
	m.client.DeleteDocument(cosmosUID)
	return nil
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) AddCreateToTransaction(ctx context.Context, transaction database.DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	cosmosObj, err := database.InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	cosmosPersistable, ok := any(newObj).(api.CosmosPersistable)
	if !ok {
		return "", fmt.Errorf("type %T does not implement CosmosPersistable", newObj)
	}

	cosmosData := cosmosPersistable.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	mockTx, ok := transaction.(*mockTransaction)
	if !ok {
		return "", fmt.Errorf("expected mockTransaction, got %T", transaction)
	}

	transactionDetails := database.CosmosDBTransactionStepDetails{
		ActionType: "Create",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosID,
	}

	mockTx.steps = append(mockTx.steps, mockTransactionStep{
		details: transactionDetails,
		execute: func() (string, json.RawMessage, error) {
			m.client.StoreDocument(cosmosID, data)
			return cosmosID, data, nil
		},
	})

	return cosmosID, nil
}

func (m *mockResourceCRUD[InternalAPIType, CosmosAPIType]) AddReplaceToTransaction(ctx context.Context, transaction database.DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	cosmosObj, err := database.InternalToCosmos[InternalAPIType, CosmosAPIType](newObj)
	if err != nil {
		return "", fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	cosmosPersistable, ok := any(newObj).(api.CosmosPersistable)
	if !ok {
		return "", fmt.Errorf("type %T does not implement CosmosPersistable", newObj)
	}

	cosmosData := cosmosPersistable.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	mockTx, ok := transaction.(*mockTransaction)
	if !ok {
		return "", fmt.Errorf("expected mockTransaction, got %T", transaction)
	}

	transactionDetails := database.CosmosDBTransactionStepDetails{
		ActionType: "Replace",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosID,
	}

	mockTx.steps = append(mockTx.steps, mockTransactionStep{
		details: transactionDetails,
		execute: func() (string, json.RawMessage, error) {
			m.client.StoreDocument(cosmosID, data)
			return cosmosID, data, nil
		},
	})

	return cosmosID, nil
}

// mockHCPClusterCRUD implements database.HCPClusterCRUD.
type mockHCPClusterCRUD struct {
	*mockResourceCRUD[api.Cluster, database.HCPCluster]
}

func newMockHCPClusterCRUD(client *MockDBClient, parentResourceID *azcorearm.ResourceID) *mockHCPClusterCRUD {
	return &mockHCPClusterCRUD{
		mockResourceCRUD: newMockResourceCRUD[api.Cluster, database.HCPCluster](client, parentResourceID, api.ClusterResourceType),
	}
}

func (m *mockHCPClusterCRUD) ExternalAuth(hcpClusterName string) database.ExternalAuthsCRUD {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return &mockExternalAuthCRUD{
		mockResourceCRUD: newMockResourceCRUD[api.ExternalAuth, database.ExternalAuth](
			m.client,
			parentResourceID,
			api.ExternalAuthResourceType,
		),
	}
}

func (m *mockHCPClusterCRUD) NodePools(hcpClusterName string) database.NodePoolsCRUD {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return &mockNodePoolsCRUD{
		mockResourceCRUD: newMockResourceCRUD[api.NodePool, database.NodePool](
			m.client,
			parentResourceID,
			api.NodePoolResourceType),
	}
}

func (m *mockHCPClusterCRUD) Controllers(hcpClusterName string) database.ResourceCRUD[api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return newMockResourceCRUD[api.Controller, database.Controller](m.client, parentResourceID, api.ClusterControllerResourceType)
}

var _ database.HCPClusterCRUD = &mockHCPClusterCRUD{}

// mockNodePoolsCRUD implements database.NodePoolsCRUD.
type mockNodePoolsCRUD struct {
	*mockResourceCRUD[api.NodePool, database.NodePool]
}

func (m *mockNodePoolsCRUD) Controllers(nodePoolName string) database.ResourceCRUD[api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			nodePoolName,
		)))

	return newMockResourceCRUD[api.Controller, database.Controller](m.client, parentResourceID, api.NodePoolControllerResourceType)
}

var _ database.NodePoolsCRUD = &mockNodePoolsCRUD{}

// mockExternalAuthCRUD implements database.ExternalAuthsCRUD.
type mockExternalAuthCRUD struct {
	*mockResourceCRUD[api.ExternalAuth, database.ExternalAuth]
}

func (m *mockExternalAuthCRUD) Controllers(externalAuthName string) database.ResourceCRUD[api.Controller] {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			externalAuthName,
		)))

	return newMockResourceCRUD[api.Controller, database.Controller](m.client, parentResourceID, api.ExternalAuthControllerResourceType)
}

var _ database.ExternalAuthsCRUD = &mockExternalAuthCRUD{}

// mockOperationCRUD implements database.OperationCRUD.
type mockOperationCRUD struct {
	*mockResourceCRUD[api.Operation, database.Operation]
}

func newMockOperationCRUD(client *MockDBClient, parentResourceID *azcorearm.ResourceID) *mockOperationCRUD {
	return &mockOperationCRUD{
		mockResourceCRUD: newMockResourceCRUD[api.Operation, database.Operation](client, parentResourceID, api.OperationStatusResourceType),
	}
}

func (m *mockOperationCRUD) ListActiveOperations(options *database.DBClientListActiveOperationDocsOptions) database.DBClientIterator[api.Operation] {
	allDocs := m.client.GetAllDocuments()

	var ids []string
	var items []*api.Operation

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		// Check resource type
		if !strings.EqualFold(typedDoc.ResourceType, api.OperationStatusResourceType.String()) {
			continue
		}

		var cosmosObj database.Operation
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		// Filter out terminal states
		status := cosmosObj.OperationProperties.Status
		if status == arm.ProvisioningStateSucceeded ||
			status == arm.ProvisioningStateFailed ||
			status == arm.ProvisioningStateCanceled {
			continue
		}

		// Apply options filters
		if options != nil {
			if options.Request != nil && cosmosObj.OperationProperties.Request != *options.Request {
				continue
			}

			if options.ExternalID != nil {
				externalID := cosmosObj.OperationProperties.ExternalID
				if externalID == nil {
					continue
				}

				if options.IncludeNestedResources {
					if !strings.HasPrefix(strings.ToLower(externalID.String()), strings.ToLower(options.ExternalID.String())) {
						continue
					}
				} else {
					if !strings.EqualFold(externalID.String(), options.ExternalID.String()) {
						continue
					}
				}
			}
		}

		internalObj, err := database.CosmosToInternalOperation(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items)
}

var _ database.OperationCRUD = &mockOperationCRUD{}

// mockSubscriptionCRUD implements database.SubscriptionCRUD.
type mockSubscriptionCRUD struct {
	client *MockDBClient
}

func newMockSubscriptionCRUD(client *MockDBClient) *mockSubscriptionCRUD {
	return &mockSubscriptionCRUD{client: client}
}

func (m *mockSubscriptionCRUD) GetByID(ctx context.Context, cosmosID string) (*arm.Subscription, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}

	data, ok := m.client.GetDocument(cosmosID)
	if !ok {
		return nil, NewNotFoundError()
	}

	var cosmosObj database.Subscription
	if err := json.Unmarshal(data, &cosmosObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	return database.CosmosToInternalSubscription(&cosmosObj)
}

func (m *mockSubscriptionCRUD) Get(ctx context.Context, resourceName string) (*arm.Subscription, error) {
	completeResourceID, err := arm.ToSubscriptionResourceID(resourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}

	cosmosID, err := api.ResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return nil, err
	}

	// Try exact match first
	result, err := m.GetByID(ctx, cosmosID)
	if err == nil {
		return result, nil
	}

	// If not found by new ID, try old lookup
	if !database.IsResponseError(err, http.StatusNotFound) {
		return nil, err
	}

	return m.GetByID(ctx, resourceName)
}

func (m *mockSubscriptionCRUD) List(ctx context.Context, options *database.DBClientListResourceDocsOptions) (database.DBClientIterator[arm.Subscription], error) {
	documents := m.client.ListDocuments(&azcorearm.SubscriptionResourceType, "")

	var ids []string
	var items []*arm.Subscription

	for _, data := range documents {
		var cosmosObj database.Subscription
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		internalObj, err := database.CosmosToInternalSubscription(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, cosmosObj.ID)
		items = append(items, internalObj)
	}

	return newMockIterator(ids, items), nil
}

func (m *mockSubscriptionCRUD) Create(ctx context.Context, newObj *arm.Subscription, options *azcosmos.ItemOptions) (*arm.Subscription, error) {
	cosmosObj, err := database.InternalToCosmosSubscription(newObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	cosmosData := newObj.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	if _, exists := m.client.GetDocument(cosmosID); exists {
		return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
	}

	m.client.StoreDocument(cosmosID, data)
	return m.GetByID(ctx, cosmosID)
}

func (m *mockSubscriptionCRUD) Replace(ctx context.Context, newObj *arm.Subscription, options *azcosmos.ItemOptions) (*arm.Subscription, error) {
	cosmosObj, err := database.InternalToCosmosSubscription(newObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	cosmosData := newObj.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	if _, exists := m.client.GetDocument(cosmosID); !exists {
		return nil, NewNotFoundError()
	}

	m.client.StoreDocument(cosmosID, data)
	return m.GetByID(ctx, cosmosID)
}

func (m *mockSubscriptionCRUD) Delete(ctx context.Context, resourceName string) error {
	completeResourceID, err := arm.ToSubscriptionResourceID(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}

	cosmosID, err := api.ResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return err
	}

	m.client.DeleteDocument(cosmosID)
	return nil
}

func (m *mockSubscriptionCRUD) AddCreateToTransaction(ctx context.Context, transaction database.DBTransaction, newObj *arm.Subscription, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	cosmosObj, err := database.InternalToCosmosSubscription(newObj)
	if err != nil {
		return "", fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	cosmosData := newObj.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	mockTx, ok := transaction.(*mockTransaction)
	if !ok {
		return "", fmt.Errorf("expected mockTransaction, got %T", transaction)
	}

	transactionDetails := database.CosmosDBTransactionStepDetails{
		ActionType: "Create",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosID,
	}

	mockTx.steps = append(mockTx.steps, mockTransactionStep{
		details: transactionDetails,
		execute: func() (string, json.RawMessage, error) {
			m.client.StoreDocument(cosmosID, data)
			return cosmosID, data, nil
		},
	})

	return cosmosID, nil
}

func (m *mockSubscriptionCRUD) AddReplaceToTransaction(ctx context.Context, transaction database.DBTransaction, newObj *arm.Subscription, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	cosmosObj, err := database.InternalToCosmosSubscription(newObj)
	if err != nil {
		return "", fmt.Errorf("failed to convert to cosmos type: %w", err)
	}

	data, err := json.Marshal(cosmosObj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cosmos object: %w", err)
	}

	cosmosData := newObj.GetCosmosData()
	cosmosID := cosmosData.GetCosmosUID()

	mockTx, ok := transaction.(*mockTransaction)
	if !ok {
		return "", fmt.Errorf("expected mockTransaction, got %T", transaction)
	}

	transactionDetails := database.CosmosDBTransactionStepDetails{
		ActionType: "Replace",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosID,
	}

	mockTx.steps = append(mockTx.steps, mockTransactionStep{
		details: transactionDetails,
		execute: func() (string, json.RawMessage, error) {
			m.client.StoreDocument(cosmosID, data)
			return cosmosID, data, nil
		},
	})

	return cosmosID, nil
}

var _ database.SubscriptionCRUD = &mockSubscriptionCRUD{}

// mockServiceProviderClusterCRUD implements database.ServiceProviderClusterCRUD.
type mockServiceProviderClusterCRUD struct {
	*mockResourceCRUD[api.ServiceProviderCluster, database.GenericDocument[api.ServiceProviderCluster]]
}

func newMockServiceProviderClusterCRUD(client *MockDBClient, parentResourceID *azcorearm.ResourceID) *mockServiceProviderClusterCRUD {
	return &mockServiceProviderClusterCRUD{
		mockResourceCRUD: newMockResourceCRUD[api.ServiceProviderCluster, database.GenericDocument[api.ServiceProviderCluster]](
			client, parentResourceID, api.ServiceProviderClusterResourceType),
	}
}

var _ database.ServiceProviderClusterCRUD = &mockServiceProviderClusterCRUD{}

// mockUntypedCRUD implements database.UntypedResourceCRUD.
type mockUntypedCRUD struct {
	client           *MockDBClient
	parentResourceID azcorearm.ResourceID
}

func newMockUntypedCRUD(client *MockDBClient, parentResourceID azcorearm.ResourceID) *mockUntypedCRUD {
	return &mockUntypedCRUD{
		client:           client,
		parentResourceID: parentResourceID,
	}
}

func (m *mockUntypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*database.TypedDocument, error) {
	if !strings.HasPrefix(strings.ToLower(resourceID.String()), strings.ToLower(m.parentResourceID.String())) {
		return nil, fmt.Errorf("resourceID %q must be a descendent of parentResourceID %q", resourceID.String(), m.parentResourceID.String())
	}

	cosmosID, err := api.ResourceIDToCosmosID(resourceID)
	if err != nil {
		return nil, err
	}

	data, ok := m.client.GetDocument(cosmosID)
	if !ok {
		// Search by resourceID
		allDocs := m.client.GetAllDocuments()

		for _, docData := range allDocs {
			var typedDoc database.TypedDocument
			if err := json.Unmarshal(docData, &typedDoc); err != nil {
				continue
			}

			var props map[string]any
			if err := json.Unmarshal(typedDoc.Properties, &props); err != nil {
				continue
			}

			resourceIDStr, ok := props["resourceId"].(string)
			if !ok {
				continue
			}

			if strings.EqualFold(resourceIDStr, resourceID.String()) {
				if err := json.Unmarshal(docData, &typedDoc); err != nil {
					continue
				}
				return &typedDoc, nil
			}
		}

		return nil, NewNotFoundError()
	}

	var typedDoc database.TypedDocument
	if err := json.Unmarshal(data, &typedDoc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	return &typedDoc, nil
}

func (m *mockUntypedCRUD) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[database.TypedDocument], error) {
	return m.listInternal(ctx, opts, true)
}

func (m *mockUntypedCRUD) ListRecursive(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[database.TypedDocument], error) {
	return m.listInternal(ctx, opts, false)
}

func (m *mockUntypedCRUD) listInternal(ctx context.Context, opts *database.DBClientListResourceDocsOptions, nonRecursive bool) (database.DBClientIterator[database.TypedDocument], error) {
	allDocs := m.client.GetAllDocuments()

	prefix := m.parentResourceID.String() + "/"
	requiredSlashes := strings.Count(m.parentResourceID.String(), "/") + 2
	if strings.EqualFold(m.parentResourceID.ResourceType.Type, "resourceGroups") {
		requiredSlashes = strings.Count(m.parentResourceID.String(), "/") + 4
	}

	var ids []string
	var items []*database.TypedDocument

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		var props map[string]any
		if err := json.Unmarshal(typedDoc.Properties, &props); err != nil {
			continue
		}

		resourceIDStr, ok := props["resourceId"].(string)
		if !ok {
			continue
		}

		if !strings.HasPrefix(strings.ToLower(resourceIDStr), strings.ToLower(prefix)) {
			continue
		}

		// For non-recursive, check slash count
		if nonRecursive {
			slashCount := strings.Count(resourceIDStr, "/")
			if slashCount != requiredSlashes {
				continue
			}
		}

		docCopy := typedDoc
		ids = append(ids, typedDoc.ID)
		items = append(items, &docCopy)
	}

	return newMockIterator(ids, items), nil
}

func (m *mockUntypedCRUD) Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	curr, err := m.Get(ctx, resourceID)
	if err != nil {
		return err
	}

	cosmosUID := curr.ID
	m.client.DeleteDocument(cosmosUID)
	return nil
}

func (m *mockUntypedCRUD) Child(resourceType azcorearm.ResourceType, resourceName string) (database.UntypedResourceCRUD, error) {
	if len(resourceName) == 0 {
		return nil, fmt.Errorf("resourceName is required")
	}

	parts := []string{m.parentResourceID.String()}

	switch {
	case strings.EqualFold(resourceType.Type, "resourcegroups"):
		// no provider needed here.
	case resourceType.Namespace == api.ProviderNamespace && m.parentResourceID.ResourceType.Namespace != api.ProviderNamespace:
		parts = append(parts,
			"providers",
			resourceType.Namespace,
		)
	case resourceType.Namespace != api.ProviderNamespace && m.parentResourceID.ResourceType.Namespace == api.ProviderNamespace:
		return nil, fmt.Errorf("cannot switch to a non-RH provider: %q", resourceType.Namespace)
	}
	parts = append(parts, resourceType.Types[len(resourceType.Types)-1])
	parts = append(parts, resourceName)

	resourcePathString := path.Join(parts...)
	newParentResourceID, err := azcorearm.ParseResourceID(resourcePathString)
	if err != nil {
		return nil, err
	}

	return newMockUntypedCRUD(m.client, *newParentResourceID), nil
}

var _ database.UntypedResourceCRUD = &mockUntypedCRUD{}
