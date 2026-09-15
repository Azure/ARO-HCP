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
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MockDocumentStore is the slice of MockDBClient that MockResourceCRUD actually
// uses. Extracting this interface lets MockResourceCRUD power both MockDBClient
// (the existing in-memory store for the regular containers), MockKubeApplierDBClient
// (the in-memory store for the kube-applier container) and MockFleetClient
// (the in-memory store for the fleet container) without code duplication.
type MockDocumentStore interface {
	GetDocument(cosmosID string) (json.RawMessage, bool)
	StoreDocument(cosmosID string, data json.RawMessage)
	DeleteDocument(cosmosID string)
	ListDocuments(resourceType *azcorearm.ResourceType, prefix string) []json.RawMessage
	GetAllDocuments() map[string]json.RawMessage
}

// MockResourceCRUD is a generic mock implementation of cosmosstorageutils.ResourceCRUD.
type MockResourceCRUD[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	client           MockDocumentStore
	parentResourceID *azcorearm.ResourceID
	resourceType     azcorearm.ResourceType
	// MakeResourceIDPath constructs the full resource ID path from a resource name.
	// This is customizable to support different resource ID construction patterns.
	MakeResourceIDPath func(resourceID string) (*azcorearm.ResourceID, error)
	// GetListPrefix returns the prefix string for listing resources.
	// This is customizable to support different listing patterns.
	GetListPrefix func() (string, error)
}

func NewMockResourceCRUD[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	client MockDocumentStore, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	m := &MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		client:           client,
		parentResourceID: parentResourceID,
		resourceType:     resourceType,
	}
	// Set default implementations
	m.MakeResourceIDPath = m.defaultMakeResourceIDPath
	m.GetListPrefix = func() (string, error) {
		prefix, err := m.defaultMakeResourceIDPath("")
		if err != nil {
			return "", err
		}
		return prefix.String() + "/", nil
	}
	return m
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) defaultMakeResourceIDPath(resourceID string) (*azcorearm.ResourceID, error) {
	if len(m.parentResourceID.SubscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}
	parts := []string{m.parentResourceID.String()}

	if !strings.EqualFold(m.parentResourceID.ResourceType.Namespace, coreapi.ProviderNamespace) {
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

func NewPreconditionFailedError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "412 Precondition Failed",
		StatusCode: http.StatusPreconditionFailed,
	}
}

// generateETag creates a new unique etag value
func generateETag() azcore.ETag {
	return azcore.ETag(uuid.New().String())
}

// injectETag injects a new etag into the document JSON
func injectETag(data json.RawMessage) (json.RawMessage, azcore.ETag, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, "", err
	}
	newETag := generateETag()
	doc["_etag"] = string(newETag)
	newData, err := json.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	return newData, newETag, nil
}

func injectID(data json.RawMessage, id string) (json.RawMessage, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	doc["id"] = id
	newData, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return newData, nil
}

// getStoredETag extracts the etag from a stored document
func getStoredETag(data json.RawMessage) azcore.ETag {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	if etag, ok := doc["_etag"].(string); ok {
		return azcore.ETag(etag)
	}
	return ""
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}

	data, ok := m.client.GetDocument(cosmosID)
	if !ok {
		return nil, cosmosstorageutils.NewNotFoundError()
	}

	if cosmosstorageutils.IsSoftDeleted(data) {
		return nil, cosmosstorageutils.NewNotFoundError()
	}

	var cosmosObj CosmosAPIType
	if err := json.Unmarshal(data, &cosmosObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	return cosmosstorageutils.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Get(ctx context.Context, resourceID string) (*InternalAPIType, error) {
	completeResourceID, err := m.MakeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	newCosmosID, err := coreapi.ResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return nil, err
	}

	return m.GetByID(ctx, newCosmosID)
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) List(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[InternalAPIType], error) {
	prefix, err := m.GetListPrefix()
	if err != nil {
		return nil, fmt.Errorf("failed to get list prefix: %w", err)
	}

	documents := m.client.ListDocuments(&m.resourceType, prefix)

	var ids []string
	var items []*InternalAPIType

	for _, data := range documents {
		var cosmosObj CosmosAPIType
		if err := json.Unmarshal(data, &cosmosObj); err != nil {
			continue
		}

		internalObj, err := cosmosstorageutils.CosmosToInternal[InternalAPIType, CosmosAPIType](&cosmosObj)
		if err != nil {
			continue
		}

		// Get the ID from the typed document
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return NewMockIterator(ids, items), nil
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if err := cosmosstorageutils.PrepareForCreate[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return nil, err
	}
	cosmosMetadata, data, err := cosmosstorageutils.SerializeItem[InternalAPIType, CosmosAPIType, InternalAPITypePointer](newObj)
	if err != nil {
		return nil, err
	}
	cosmosID := cosmosMetadata.GetCosmosUID()

	// Check for existing
	if _, exists := m.client.GetDocument(cosmosID); exists {
		return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
	}

	// Inject a new etag and store
	dataWithETag, _, err := injectETag(data)
	if err != nil {
		return nil, fmt.Errorf("failed to inject etag: %w", err)
	}
	m.client.StoreDocument(cosmosID, dataWithETag)

	// Read back the stored object
	return m.GetByID(ctx, cosmosID)
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Replace(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	if err := cosmosstorageutils.PrepareForReplace[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return nil, err
	}
	cosmosMetadata, data, err := cosmosstorageutils.SerializeItem[InternalAPIType, CosmosAPIType, InternalAPITypePointer](newObj)
	if err != nil {
		return nil, err
	}
	resourceName := cosmosMetadata.GetResourceID().Name
	expectedETag := cosmosMetadata.CosmosETag

	oldObj, err := m.Get(ctx, resourceName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	storedETag := any(oldObj).(coreapi.CosmosPersistable).GetCosmosData().CosmosETag
	existingCosmosID := any(oldObj).(coreapi.CosmosPersistable).GetCosmosData().GetCosmosUID()

	if storedETag != expectedETag {
		return nil, NewPreconditionFailedError()
	}

	// Inject a new etag and store
	dataWithETag, _, err := injectETag(data)
	if err != nil {
		return nil, fmt.Errorf("failed to inject etag: %w", err)
	}
	dataWithETag, err = injectID(dataWithETag, existingCosmosID)
	if err != nil {
		return nil, fmt.Errorf("failed to inject ID: %w", err)
	}
	m.client.StoreDocument(existingCosmosID, dataWithETag)

	// Read back the stored object
	return m.Get(ctx, resourceName)
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Delete(ctx context.Context, resourceID string) error {
	completeResourceID, err := m.MakeResourceIDPath(resourceID)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}

	cosmosUID, err := coreapi.ResourceIDToCosmosID(completeResourceID)
	if err != nil {
		return err
	}

	return mockSoftDelete(m.client, cosmosUID)
}

func mockSoftDelete(store MockDocumentStore, cosmosID string) error {
	data, ok := store.GetDocument(cosmosID)
	if !ok {
		return nil
	}

	var doc cosmosstorageutils.GenericDocument[map[string]interface{}]
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("failed to unmarshal document for soft delete: %w", err)
	}

	if doc.DeletionTimestamp != nil {
		return nil
	}

	if err := cosmosstorageutils.SetSoftDeleteFields(&doc, time.Now()); err != nil {
		return utils.TrackError(err)
	}

	modified, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal soft-deleted document: %w", err)
	}

	dataWithETag, _, err := injectETag(json.RawMessage(modified))
	if err != nil {
		return fmt.Errorf("failed to inject etag: %w", err)
	}
	store.StoreDocument(cosmosID, dataWithETag)
	return nil
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddCreateToTransaction(ctx context.Context, transaction cosmosstorageutils.DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	if err := cosmosstorageutils.PrepareForCreate[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return "", err
	}
	cosmosMetadata, data, err := cosmosstorageutils.SerializeItem[InternalAPIType, CosmosAPIType, InternalAPITypePointer](newObj)
	if err != nil {
		return "", err
	}
	cosmosID := cosmosMetadata.GetCosmosUID()

	mockTx, ok := transaction.(*mockTransaction)
	if !ok {
		return "", fmt.Errorf("expected mockTransaction, got %T", transaction)
	}

	transactionDetails := cosmosstorageutils.CosmosDBTransactionStepDetails{
		ActionType: "Create",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosID,
	}

	mockTx.steps = append(mockTx.steps, mockTransactionStep{
		details: transactionDetails,
		execute: func() (string, json.RawMessage, error) {
			// Inject a new etag and store
			dataWithETag, _, err := injectETag(data)
			if err != nil {
				return "", nil, fmt.Errorf("failed to inject etag: %w", err)
			}
			m.client.StoreDocument(cosmosID, dataWithETag)
			return cosmosID, dataWithETag, nil
		},
	})

	return cosmosID, nil
}

func (m *MockResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddReplaceToTransaction(ctx context.Context, transaction cosmosstorageutils.DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	if err := cosmosstorageutils.PrepareForReplace[InternalAPIType, InternalAPITypePointer](newObj); err != nil {
		return "", err
	}
	cosmosMetadata, data, err := cosmosstorageutils.SerializeItem[InternalAPIType, CosmosAPIType, InternalAPITypePointer](newObj)
	if err != nil {
		return "", err
	}
	cosmosID := cosmosMetadata.GetCosmosUID()
	expectedETag := cosmosMetadata.CosmosETag

	mockTx, ok := transaction.(*mockTransaction)
	if !ok {
		return "", fmt.Errorf("expected mockTransaction, got %T", transaction)
	}

	transactionDetails := cosmosstorageutils.CosmosDBTransactionStepDetails{
		ActionType: "Replace",
		GoType:     fmt.Sprintf("%T", newObj),
		CosmosID:   cosmosID,
	}

	mockTx.steps = append(mockTx.steps, mockTransactionStep{
		details: transactionDetails,
		execute: func() (string, json.RawMessage, error) {
			existingData, exists := m.client.GetDocument(cosmosID)
			if !exists {
				return "", nil, cosmosstorageutils.NewNotFoundError()
			}
			storedETag := getStoredETag(existingData)
			if storedETag != expectedETag {
				return "", nil, NewPreconditionFailedError()
			}
			// Inject a new etag and store
			dataWithETag, _, err := injectETag(data)
			if err != nil {
				return "", nil, fmt.Errorf("failed to inject etag: %w", err)
			}
			m.client.StoreDocument(cosmosID, dataWithETag)
			return cosmosID, dataWithETag, nil
		},
	})

	return cosmosID, nil
}

// mockHCPClusterCRUD implements corecosmosstorage.HCPClusterCRUD.
type mockHCPClusterCRUD struct {
	*MockResourceCRUD[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftCluster]]
}

func newMockHCPClusterCRUD(client *MockResourcesDBClient, parentResourceID *azcorearm.ResourceID) *mockHCPClusterCRUD {
	return &mockHCPClusterCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftCluster]](client, parentResourceID, coreapi.ClusterResourceType),
	}
}

func (m *mockHCPClusterCRUD) ExternalAuth(hcpClusterName string) corecosmosstorage.ExternalAuthsCRUD {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return &mockExternalAuthCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.HCPOpenShiftClusterExternalAuth, *coreapi.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterExternalAuth]](
			m.client,
			parentResourceID,
			coreapi.ExternalAuthResourceType,
		),
	}
}

func (m *mockHCPClusterCRUD) NodePools(hcpClusterName string) corecosmosstorage.NodePoolsCRUD {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return &mockNodePoolsCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.HCPOpenShiftClusterNodePool, *coreapi.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterNodePool]](
			m.client,
			parentResourceID,
			coreapi.NodePoolResourceType),
	}
}

func (m *mockHCPClusterCRUD) Controllers(hcpClusterName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return NewMockResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](m.client, parentResourceID, coreapi.ClusterControllerResourceType)
}

func (m *mockHCPClusterCRUD) ManagementClusterContents(hcpClusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			m.resourceType.Namespace,
			m.resourceType.Type,
			hcpClusterName)))

	return newMockManagementClusterContentCRUD(m.client, parentResourceID, coreapi.ClusterScopedManagementClusterContentResourceType)
}

func (m *mockHCPClusterCRUD) SystemAdminCredentialRequests(hcpClusterName string) corecosmosstorage.SystemAdminCredentialRequestsCRUD {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			coreapi.ClusterResourceType.Namespace,
			coreapi.ClusterResourceType.Type,
			hcpClusterName)))

	return &mockSystemAdminCredentialRequestsCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.SystemAdminCredentialRequest, *coreapi.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRequest]](m.client, clusterResourceID, coreapi.SystemAdminCredentialRequestResourceType),
	}
}

func (m *mockHCPClusterCRUD) SystemAdminCredentialRevocations(hcpClusterName string) corecosmosstorage.SystemAdminCredentialRevocationsCRUD {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			"providers",
			coreapi.ClusterResourceType.Namespace,
			coreapi.ClusterResourceType.Type,
			hcpClusterName)))

	return &mockSystemAdminCredentialRevocationsCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.SystemAdminCredentialRevocation, *coreapi.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRevocation]](m.client, clusterResourceID, coreapi.SystemAdminCredentialRevocationResourceType),
	}
}

var _ corecosmosstorage.HCPClusterCRUD = &mockHCPClusterCRUD{}

// mockNodePoolsCRUD implements corecosmosstorage.NodePoolsCRUD.
type mockNodePoolsCRUD struct {
	*MockResourceCRUD[coreapi.HCPOpenShiftClusterNodePool, *coreapi.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterNodePool]]
}

func (m *mockNodePoolsCRUD) Controllers(nodePoolName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			nodePoolName,
		)))

	return NewMockResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](m.client, parentResourceID, coreapi.NodePoolControllerResourceType)
}

func (m *mockNodePoolsCRUD) ManagementClusterContents(nodePoolName string) cosmosstorageutils.ResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			nodePoolName,
		)))

	return newMockManagementClusterContentCRUD(m.client, parentResourceID, coreapi.NodePoolScopedManagementClusterContentResourceType)
}

var _ corecosmosstorage.NodePoolsCRUD = &mockNodePoolsCRUD{}

// mockExternalAuthCRUD implements corecosmosstorage.ExternalAuthsCRUD.
type mockExternalAuthCRUD struct {
	*MockResourceCRUD[coreapi.HCPOpenShiftClusterExternalAuth, *coreapi.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftClusterExternalAuth]]
}

func (m *mockExternalAuthCRUD) Controllers(externalAuthName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			externalAuthName,
		)))

	return NewMockResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](m.client, parentResourceID, coreapi.ExternalAuthControllerResourceType)
}

var _ corecosmosstorage.ExternalAuthsCRUD = &mockExternalAuthCRUD{}

type mockSystemAdminCredentialRequestsCRUD struct {
	*MockResourceCRUD[coreapi.SystemAdminCredentialRequest, *coreapi.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRequest]]
}

func (m *mockSystemAdminCredentialRequestsCRUD) Controllers(credentialRequestName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			credentialRequestName,
		)))

	return NewMockResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](m.client, parentResourceID, coreapi.SystemAdminCredentialRequestControllerResourceType)
}

var _ corecosmosstorage.SystemAdminCredentialRequestsCRUD = &mockSystemAdminCredentialRequestsCRUD{}

type mockSystemAdminCredentialRevocationsCRUD struct {
	*MockResourceCRUD[coreapi.SystemAdminCredentialRevocation, *coreapi.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[coreapi.SystemAdminCredentialRevocation]]
}

func (m *mockSystemAdminCredentialRevocationsCRUD) Controllers(revocationName string) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		path.Join(
			m.parentResourceID.String(),
			m.resourceType.Types[len(m.resourceType.Types)-1],
			revocationName,
		)))

	return NewMockResourceCRUD[coreapi.Controller, *coreapi.Controller, cosmosstorageutils.GenericDocument[coreapi.Controller]](m.client, parentResourceID, coreapi.SystemAdminCredentialRevocationControllerResourceType)
}

var _ corecosmosstorage.SystemAdminCredentialRevocationsCRUD = &mockSystemAdminCredentialRevocationsCRUD{}

// mockOperationCRUD implements corecosmosstorage.OperationCRUD.
type mockOperationCRUD struct {
	*MockResourceCRUD[coreapi.Operation, *coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]]
}

func newMockOperationCRUD(client *MockResourcesDBClient, parentResourceID *azcorearm.ResourceID) *mockOperationCRUD {
	return &mockOperationCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.Operation, *coreapi.Operation, cosmosstorageutils.GenericDocument[coreapi.Operation]](client, parentResourceID, coreapi.OperationStatusResourceType),
	}
}

func (m *mockOperationCRUD) ListActiveOperations(options *corecosmosstorage.ResourcesDBClientListActiveOperationDocsOptions) cosmosstorageutils.DBClientIterator[coreapi.Operation] {
	allDocs := m.client.GetAllDocuments()

	var ids []string
	var items []*coreapi.Operation

	for _, data := range allDocs {
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		// Check resource type
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

		// Filter out terminal states unless IncludeTerminal is set
		if options == nil || !options.IncludeTerminal {
			status := cosmosObj.Content.Status
			if status == coreapi.ProvisioningStateSucceeded ||
				status == coreapi.ProvisioningStateFailed ||
				status == coreapi.ProvisioningStateCanceled {
				continue
			}
		}

		// Apply options filters
		if options != nil {
			if options.Request != nil && cosmosObj.Content.Request != *options.Request {
				continue
			}

			if options.ExternalID != nil {
				externalID := cosmosObj.Content.ExternalID
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

		internalObj, err := cosmosstorageutils.CosmosGenericToInternal(&cosmosObj)
		if err != nil {
			continue
		}

		ids = append(ids, typedDoc.ID)
		items = append(items, internalObj)
	}

	return NewMockIterator(ids, items)
}

var _ corecosmosstorage.OperationCRUD = &mockOperationCRUD{}

// mockSubscriptionCRUD implements cosmosstorageutils.ResourceCRUD[coreapi.Subscription, *coreapi.Subscription].
// It embeds MockResourceCRUD with customized MakeResourceIDPath and GetListPrefix
// functions for subscription-specific resource ID construction.
type mockSubscriptionCRUD struct {
	*MockResourceCRUD[coreapi.Subscription, *coreapi.Subscription, cosmosstorageutils.GenericDocument[coreapi.Subscription]]
}

func newMockSubscriptionCRUD(client *MockResourcesDBClient) *mockSubscriptionCRUD {
	base := NewMockResourceCRUD[coreapi.Subscription, *coreapi.Subscription, cosmosstorageutils.GenericDocument[coreapi.Subscription]](
		client, nil, azcorearm.SubscriptionResourceType)

	// Override MakeResourceIDPath for subscription-specific resource ID construction
	base.MakeResourceIDPath = func(resourceID string) (*azcorearm.ResourceID, error) {
		return coreapi.ToSubscriptionResourceID(resourceID)
	}

	// Override GetListPrefix for subscription-specific listing (no parent prefix)
	base.GetListPrefix = func() (string, error) {
		return "/subscriptions/", nil
	}

	return &mockSubscriptionCRUD{
		MockResourceCRUD: base,
	}
}

var _ cosmosstorageutils.ResourceCRUD[coreapi.Subscription, *coreapi.Subscription] = &mockSubscriptionCRUD{}

// mockServiceProviderClusterCRUD implements cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster].
type mockServiceProviderClusterCRUD struct {
	*MockResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderCluster]]
}

func newMockServiceProviderClusterCRUD(client *MockResourcesDBClient, parentResourceID *azcorearm.ResourceID) *mockServiceProviderClusterCRUD {
	return &mockServiceProviderClusterCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderCluster]](
			client, parentResourceID, coreapi.ServiceProviderClusterResourceType),
	}
}

var _ cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] = &mockServiceProviderClusterCRUD{}

// mockServiceProviderNodePoolCRUD implements cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool].
type mockServiceProviderNodePoolCRUD struct {
	*MockResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderNodePool]]
}

func newMockServiceProviderNodePoolCRUD(client *MockResourcesDBClient, parentResourceID *azcorearm.ResourceID) *mockServiceProviderNodePoolCRUD {
	return &mockServiceProviderNodePoolCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderNodePool]](
			client, parentResourceID, coreapi.ServiceProviderNodePoolResourceType),
	}
}

var _ cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool] = &mockServiceProviderNodePoolCRUD{}

// mockServiceProviderExternalAuthCRUD implements cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderExternalAuth, *coreapi.ServiceProviderExternalAuth].
type mockServiceProviderExternalAuthCRUD struct {
	*MockResourceCRUD[coreapi.ServiceProviderExternalAuth, *coreapi.ServiceProviderExternalAuth, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderExternalAuth]]
}

func newMockServiceProviderExternalAuthCRUD(client *MockResourcesDBClient, parentResourceID *azcorearm.ResourceID) *mockServiceProviderExternalAuthCRUD {
	return &mockServiceProviderExternalAuthCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.ServiceProviderExternalAuth, *coreapi.ServiceProviderExternalAuth, cosmosstorageutils.GenericDocument[coreapi.ServiceProviderExternalAuth]](
			client, parentResourceID, coreapi.ServiceProviderExternalAuthResourceType),
	}
}

var _ cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderExternalAuth, *coreapi.ServiceProviderExternalAuth] = &mockServiceProviderExternalAuthCRUD{}

// mockManagementClusterContentCRUD implements cosmosstorageutils.ResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent].
type mockManagementClusterContentCRUD struct {
	*MockResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent, cosmosstorageutils.GenericDocument[coreapi.ManagementClusterContent]]
}

func newMockManagementClusterContentCRUD(client MockDocumentStore, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) *mockManagementClusterContentCRUD {
	return &mockManagementClusterContentCRUD{
		MockResourceCRUD: NewMockResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent, cosmosstorageutils.GenericDocument[coreapi.ManagementClusterContent]](
			client, parentResourceID, resourceType),
	}
}

var _ cosmosstorageutils.ResourceCRUD[coreapi.ManagementClusterContent, *coreapi.ManagementClusterContent] = &mockManagementClusterContentCRUD{}

// mockUntypedCRUD implements cosmosstorageutils.UntypedResourceCRUD.
type mockUntypedCRUD struct {
	client           *MockResourcesDBClient
	parentResourceID azcorearm.ResourceID
}

func newMockUntypedCRUD(client *MockResourcesDBClient, parentResourceID azcorearm.ResourceID) *mockUntypedCRUD {
	return &mockUntypedCRUD{
		client:           client,
		parentResourceID: parentResourceID,
	}
}

func (m *mockUntypedCRUD) Get(ctx context.Context, resourceID *azcorearm.ResourceID) (*cosmosstorageutils.TypedDocument, error) {
	if !strings.HasPrefix(strings.ToLower(resourceID.String()), strings.ToLower(m.parentResourceID.String())) {
		return nil, fmt.Errorf("resourceID %q must be a descendent of parentResourceID %q", resourceID.String(), m.parentResourceID.String())
	}

	newCosmosID, err := coreapi.ResourceIDToCosmosID(resourceID)
	if err != nil {
		return nil, err
	}

	data, ok := m.client.GetDocument(newCosmosID)
	if ok {
		if cosmosstorageutils.IsSoftDeleted(data) {
			return nil, cosmosstorageutils.NewNotFoundError()
		}
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			return nil, fmt.Errorf("failed to unmarshal document: %w", err)
		}
		return cosmosstorageutils.CosmosToInternal[cosmosstorageutils.TypedDocument, cosmosstorageutils.TypedDocument](&typedDoc)
	}

	return nil, cosmosstorageutils.NewNotFoundError()
}

func (m *mockUntypedCRUD) List(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	return m.listInternal(ctx, opts, true)
}

func (m *mockUntypedCRUD) ListRecursive(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	return m.listInternal(ctx, opts, false)
}

func (m *mockUntypedCRUD) listInternal(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions, nonRecursive bool) (cosmosstorageutils.DBClientIterator[cosmosstorageutils.TypedDocument], error) {
	allDocs := m.client.GetAllDocuments()

	prefix := m.parentResourceID.String() + "/"
	requiredSlashes := strings.Count(m.parentResourceID.String(), "/") + 2
	if strings.EqualFold(m.parentResourceID.ResourceType.Type, "resourceGroups") {
		requiredSlashes = strings.Count(m.parentResourceID.String(), "/") + 4
	}

	var ids []string
	var items []*cosmosstorageutils.TypedDocument

	for _, data := range allDocs {
		var typedDoc cosmosstorageutils.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		if typedDoc.ResourceID != nil && !strings.HasPrefix(strings.ToLower(typedDoc.ResourceID.String()), strings.ToLower(prefix)) {
			continue
		}

		if typedDoc.DeletionTimestamp != nil {
			continue
		}

		// For non-recursive, check slash count
		if nonRecursive {
			slashCount := strings.Count(typedDoc.ResourceID.String(), "/")
			if slashCount != requiredSlashes {
				continue
			}
		}

		docCopy := typedDoc
		docPointer, err := cosmosstorageutils.CosmosToInternal[cosmosstorageutils.TypedDocument, cosmosstorageutils.TypedDocument](&docCopy)
		if err != nil {
			continue
		}
		ids = append(ids, docPointer.ID)
		items = append(items, docPointer)
	}

	return NewMockIterator(ids, items), nil
}

func (m *mockUntypedCRUD) Delete(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	cosmosUID, err := coreapi.ResourceIDToCosmosID(resourceID)
	if err != nil {
		return err
	}
	return mockSoftDelete(m.client, cosmosUID)
}

func (m *mockUntypedCRUD) DeleteByCosmosID(ctx context.Context, partitionKey, cosmosID string) error {
	return mockSoftDelete(m.client, cosmosID)
}

func (m *mockUntypedCRUD) Child(resourceType azcorearm.ResourceType, resourceName string) (cosmosstorageutils.UntypedResourceCRUD, error) {
	if len(resourceName) == 0 {
		return nil, fmt.Errorf("resourceName is required")
	}

	parts := []string{m.parentResourceID.String()}

	switch {
	case strings.EqualFold(resourceType.Type, "resourcegroups"):
		// no provider needed here.
	case resourceType.Namespace == coreapi.ProviderNamespace && m.parentResourceID.ResourceType.Namespace != coreapi.ProviderNamespace:
		parts = append(parts,
			"providers",
			resourceType.Namespace,
		)
	case resourceType.Namespace != coreapi.ProviderNamespace && m.parentResourceID.ResourceType.Namespace == coreapi.ProviderNamespace:
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

var _ cosmosstorageutils.UntypedResourceCRUD = &mockUntypedCRUD{}
