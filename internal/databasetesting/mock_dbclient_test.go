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
	"path/filepath"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

func TestMockDBClient_LoadFromDirectory(t *testing.T) {
	// Find a test directory with cosmos-record context data
	testDataDir := filepath.Join("..", "..", "test-integration", "frontend", "artifacts", "FrontendCRUD", "NodePool", "read-old-data", "01-load-old-data")

	mock := NewMockDBClient()
	err := mock.LoadFromDirectory(testDataDir)
	if err != nil {
		t.Fatalf("Failed to load test data from %s: %v", testDataDir, err)
	}

	// Verify that documents were loaded
	allDocs := mock.GetAllDocuments()
	docCount := len(allDocs)

	if docCount == 0 {
		t.Fatal("Expected documents to be loaded, but got 0")
	}

	t.Logf("Loaded %d documents from %s", docCount, testDataDir)

	// Verify we can read different resource types
	foundCluster := false
	foundNodePool := false
	foundSubscription := false
	foundOperation := false

	for _, data := range allDocs {
		var typedDoc database.TypedDocument
		if err := json.Unmarshal(data, &typedDoc); err != nil {
			continue
		}

		switch {
		case apihelpers.ResourceTypeStringEqual(typedDoc.ResourceType, api.ClusterResourceType):
			foundCluster = true
		case apihelpers.ResourceTypeStringEqual(typedDoc.ResourceType, api.NodePoolResourceType):
			foundNodePool = true
		case apihelpers.ResourceTypeStringEqual(typedDoc.ResourceType, azcorearm.SubscriptionResourceType):
			foundSubscription = true
		case apihelpers.ResourceTypeStringEqual(typedDoc.ResourceType, api.OperationStatusResourceType):
			foundOperation = true
		}
	}

	if !foundCluster {
		t.Error("Expected to find a cluster document")
	}
	if !foundNodePool {
		t.Error("Expected to find a node pool document")
	}
	if !foundSubscription {
		t.Error("Expected to find a subscription document")
	}
	if !foundOperation {
		t.Error("Expected to find an operation document")
	}
}

func TestMockDBClient_LoadAndQuery(t *testing.T) {
	// Load test data
	testDataDir := filepath.Join("..", "..", "test-integration", "frontend", "artifacts", "FrontendCRUD", "NodePool", "read-old-data", "01-load-old-data")

	mock := NewMockDBClient()
	err := mock.LoadFromDirectory(testDataDir)
	if err != nil {
		t.Fatalf("Failed to load test data: %v", err)
	}

	ctx := context.Background()

	// Try to query the loaded data
	// The test data contains clusters in the subscription 6b690bec-0c16-4ecb-8f67-781caf40bba7
	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"

	clusterCRUD := mock.HCPClusters(subscriptionID, resourceGroupName)

	// List clusters
	iter, err := clusterCRUD.List(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to list clusters: %v", err)
	}

	count := 0
	for _, item := range iter.Items(ctx) {
		if item != nil {
			count++
			t.Logf("Found cluster: %s", item.Name)
		}
	}

	if iter.GetError() != nil {
		t.Fatalf("Iterator error: %v", iter.GetError())
	}

	t.Logf("Found %d clusters", count)
}

func TestMockDBClient_CRUD_Cluster(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	// Create a cluster
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123")
	if err != nil {
		t.Fatalf("Failed to create internal ID: %v", err)
	}

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: clusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  internalID,
		},
	}

	clusterCRUD := mock.HCPClusters(subscriptionID, resourceGroupName)

	// Create
	created, err := clusterCRUD.Create(ctx, cluster, nil)
	if err != nil {
		t.Fatalf("Failed to create cluster: %v", err)
	}

	if created.Name != clusterName {
		t.Errorf("Expected cluster name %s, got %s", clusterName, created.Name)
	}

	// Get
	retrieved, err := clusterCRUD.Get(ctx, clusterName)
	if err != nil {
		t.Fatalf("Failed to get cluster: %v", err)
	}

	if retrieved.Name != clusterName {
		t.Errorf("Expected cluster name %s, got %s", clusterName, retrieved.Name)
	}

	// List
	iter, err := clusterCRUD.List(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to list clusters: %v", err)
	}

	count := 0
	for _, item := range iter.Items(ctx) {
		if item != nil {
			count++
		}
	}

	if iter.GetError() != nil {
		t.Fatalf("Iterator error: %v", iter.GetError())
	}

	if count != 1 {
		t.Errorf("Expected 1 cluster in list, got %d", count)
	}

	// Delete
	err = clusterCRUD.Delete(ctx, clusterName)
	if err != nil {
		t.Fatalf("Failed to delete cluster: %v", err)
	}

	// Verify deletion
	_, err = clusterCRUD.Get(ctx, clusterName)
	if !database.IsResponseError(err, 404) {
		t.Errorf("Expected 404 after deletion, got: %v", err)
	}
}

func TestMockDBClient_CRUD_Operation(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"

	// Create an operation
	operationID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/op-123"))

	externalID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))

	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op-123"))

	now := time.Now().UTC()
	operation := &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		OperationID:        operationID,
		ExternalID:         externalID,
		Request:            api.OperationRequestCreate,
		Status:             arm.ProvisioningStateAccepted,
		StartTime:          now,
		LastTransitionTime: now,
	}

	operationCRUD := mock.Operations(subscriptionID)

	// Create
	created, err := operationCRUD.Create(ctx, operation, nil)
	if err != nil {
		t.Fatalf("Failed to create operation: %v", err)
	}

	if created.OperationID.Name != operationID.Name {
		t.Errorf("Expected operation ID %s, got %s", operationID.Name, created.OperationID.Name)
	}

	// List active operations
	iter := operationCRUD.ListActiveOperations(nil)

	count := 0
	for _, item := range iter.Items(ctx) {
		if item != nil {
			count++
		}
	}

	if iter.GetError() != nil {
		t.Fatalf("Iterator error: %v", iter.GetError())
	}

	if count != 1 {
		t.Errorf("Expected 1 active operation, got %d", count)
	}

	// List with filter
	createRequest := api.OperationRequestCreate
	iterFiltered := operationCRUD.ListActiveOperations(&database.DBClientListActiveOperationDocsOptions{
		Request: &createRequest,
	})

	countFiltered := 0
	for _, item := range iterFiltered.Items(ctx) {
		if item != nil {
			countFiltered++
		}
	}

	if iterFiltered.GetError() != nil {
		t.Fatalf("Iterator error: %v", iterFiltered.GetError())
	}

	if countFiltered != 1 {
		t.Errorf("Expected 1 active operation with filter, got %d", countFiltered)
	}
}

func TestMockDBClient_CRUD_Subscription(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(subscriptionID))

	registrationDate := "2025-01-01T00:00:00Z"
	subscription := &arm.Subscription{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: subscriptionResourceID,
		},
		ResourceID:       subscriptionResourceID,
		State:            arm.SubscriptionStateRegistered,
		RegistrationDate: &registrationDate,
	}

	subscriptionCRUD := mock.Subscriptions()

	// Create
	created, err := subscriptionCRUD.Create(ctx, subscription, nil)
	if err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	if created.State != arm.SubscriptionStateRegistered {
		t.Errorf("Expected state %s, got %s", arm.SubscriptionStateRegistered, created.State)
	}

	// Get
	retrieved, err := subscriptionCRUD.Get(ctx, subscriptionID)
	if err != nil {
		t.Fatalf("Failed to get subscription: %v", err)
	}

	if retrieved.State != arm.SubscriptionStateRegistered {
		t.Errorf("Expected state %s, got %s", arm.SubscriptionStateRegistered, retrieved.State)
	}

	// Replace
	subscription.State = arm.SubscriptionStateSuspended
	replaced, err := subscriptionCRUD.Replace(ctx, subscription, nil)
	if err != nil {
		t.Fatalf("Failed to replace subscription: %v", err)
	}

	if replaced.State != arm.SubscriptionStateSuspended {
		t.Errorf("Expected state %s, got %s", arm.SubscriptionStateSuspended, replaced.State)
	}

	// Delete
	err = subscriptionCRUD.Delete(ctx, subscriptionID)
	if err != nil {
		t.Fatalf("Failed to delete subscription: %v", err)
	}

	// Verify deletion
	_, err = subscriptionCRUD.Get(ctx, subscriptionID)
	if !database.IsResponseError(err, 404) {
		t.Errorf("Expected 404 after deletion, got: %v", err)
	}
}

func TestMockDBClient_Transaction(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	// Create a cluster via transaction
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123")
	if err != nil {
		t.Fatalf("Failed to create internal ID: %v", err)
	}

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: clusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  internalID,
		},
	}

	transaction := mock.NewTransaction(subscriptionID)
	clusterCRUD := mock.HCPClusters(subscriptionID, resourceGroupName)

	_, err = clusterCRUD.AddCreateToTransaction(ctx, transaction, cluster, nil)
	if err != nil {
		t.Fatalf("Failed to add create to transaction: %v", err)
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to execute transaction: %v", err)
	}

	// Verify cluster was created
	retrieved, err := clusterCRUD.Get(ctx, clusterName)
	if err != nil {
		t.Fatalf("Failed to get cluster after transaction: %v", err)
	}

	if retrieved.Name != clusterName {
		t.Errorf("Expected cluster name %s, got %s", clusterName, retrieved.Name)
	}
}

func TestMockDBClient_UntypedCRUD(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	// First create a cluster using the typed CRUD
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123")
	if err != nil {
		t.Fatalf("Failed to create internal ID: %v", err)
	}

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: clusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  internalID,
		},
	}

	clusterCRUD := mock.HCPClusters(subscriptionID, resourceGroupName)
	_, err = clusterCRUD.Create(ctx, cluster, nil)
	if err != nil {
		t.Fatalf("Failed to create cluster: %v", err)
	}

	// Now use untyped CRUD to access it
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName))

	untypedCRUD, err := mock.UntypedCRUD(*parentResourceID)
	if err != nil {
		t.Fatalf("Failed to get untyped CRUD: %v", err)
	}

	// Get the cluster
	retrieved, err := untypedCRUD.Get(ctx, clusterResourceID)
	if err != nil {
		t.Fatalf("Failed to get cluster via untyped CRUD: %v", err)
	}

	if !apihelpers.ResourceTypeStringEqual(retrieved.ResourceType, api.ClusterResourceType) {
		t.Errorf("Expected resource type %s, got %s", api.ClusterResourceType.String(), retrieved.ResourceType)
	}
}

func TestMockDBClient_StoreAndRetrieveRawDocument(t *testing.T) {
	mock := NewMockDBClient()

	cosmosID := "test-cosmos-id"
	testData := map[string]any{
		"id":           cosmosID,
		"partitionKey": "test-partition",
		"resourceType": "TestType",
		"properties": map[string]any{
			"foo": "bar",
		},
	}

	data, err := json.Marshal(testData)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	// Store
	mock.StoreDocument(cosmosID, data)

	// Retrieve
	retrieved, ok := mock.GetDocument(cosmosID)
	if !ok {
		t.Fatal("Document not found")
	}

	var result map[string]any
	if err := json.Unmarshal(retrieved, &result); err != nil {
		t.Fatalf("Failed to unmarshal retrieved data: %v", err)
	}

	if result["id"] != cosmosID {
		t.Errorf("Expected id %s, got %s", cosmosID, result["id"])
	}

	// Delete
	mock.DeleteDocument(cosmosID)

	// Verify deletion
	_, ok = mock.GetDocument(cosmosID)
	if ok {
		t.Error("Document should have been deleted")
	}
}

func TestMockDBClient_Clear(t *testing.T) {
	mock := NewMockDBClient()

	// Store some documents
	mock.StoreDocument("doc1", json.RawMessage(`{"id": "doc1"}`))
	mock.StoreDocument("doc2", json.RawMessage(`{"id": "doc2"}`))

	countBefore := len(mock.GetAllDocuments())

	if countBefore != 2 {
		t.Errorf("Expected 2 documents before clear, got %d", countBefore)
	}

	// Clear
	mock.Clear()

	countAfter := len(mock.GetAllDocuments())

	if countAfter != 0 {
		t.Errorf("Expected 0 documents after clear, got %d", countAfter)
	}
}

func TestMockDBClient_ServiceProviderCluster_ETagConditionalReplace(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	// Create the resource ID for the ServiceProviderCluster
	serviceProviderClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/serviceProviderClusters/default"))

	serviceProviderCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: serviceProviderClusterResourceID,
		},
	}

	spClusterCRUD := mock.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)

	// Create the document
	created, err := spClusterCRUD.Create(ctx, serviceProviderCluster, nil)
	if err != nil {
		t.Fatalf("Failed to create service provider cluster: %v", err)
	}

	// The created object should have an etag
	if len(created.CosmosETag) == 0 {
		t.Fatal("Expected created object to have an etag")
	}
	originalETag := created.CosmosETag

	t.Run("positive case - matching etag succeeds", func(t *testing.T) {
		// Get the current version to ensure we have the correct etag
		current, err := spClusterCRUD.Get(ctx, "default")
		if err != nil {
			t.Fatalf("Failed to get service provider cluster: %v", err)
		}

		// Update with the correct etag
		loadBalancerResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID +
				"/resourceGroups/" + resourceGroupName +
				"/providers/Microsoft.Network/loadBalancers/my-lb"))

		current.LoadBalancerResourceID = loadBalancerResourceID

		replaced, err := spClusterCRUD.Replace(ctx, current, nil)
		if err != nil {
			t.Fatalf("Replace with matching etag should succeed, got error: %v", err)
		}

		if replaced.LoadBalancerResourceID == nil || replaced.LoadBalancerResourceID.String() != loadBalancerResourceID.String() {
			t.Errorf("Expected LoadBalancerResourceID to be updated")
		}

		// The new etag should be different from the original
		if replaced.CosmosETag == originalETag {
			t.Errorf("Expected new etag after replace, got same etag")
		}
	})

	t.Run("negative case - mismatched etag fails", func(t *testing.T) {
		// Get the current version
		current, err := spClusterCRUD.Get(ctx, "default")
		if err != nil {
			t.Fatalf("Failed to get service provider cluster: %v", err)
		}

		// Set an incorrect etag
		current.CosmosETag = "wrong-etag-value"

		_, err = spClusterCRUD.Replace(ctx, current, nil)
		if err == nil {
			t.Fatal("Replace with wrong etag should fail")
		}

		if !database.IsResponseError(err, 412) {
			t.Errorf("Expected 412 Precondition Failed, got: %v", err)
		}
	})
}

func TestMockDBClient_Controller_ETagConditionalReplace(t *testing.T) {
	mock := NewMockDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	controllerName := "test-controller"

	// Create the resource ID for the Controller
	// Note: The controller resource type is "hcpOpenShiftControllers" not "controller"
	controllerResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/hcpOpenShiftControllers/" + controllerName))

	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	controller := &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: controllerResourceID,
		},
		ResourceID: controllerResourceID,
		ExternalID: clusterResourceID,
		Status: api.ControllerStatus{
			Conditions: []api.Condition{
				{
					Type:               "Degraded",
					Status:             api.ConditionFalse,
					LastTransitionTime: time.Now(),
					Reason:             "AllHealthy",
					Message:            "All systems operational",
				},
			},
		},
	}

	// Get controller CRUD via HCPClusters -> Controllers
	clusterCRUD := mock.HCPClusters(subscriptionID, resourceGroupName)
	controllerCRUD := clusterCRUD.Controllers(clusterName)

	// Create the document
	created, err := controllerCRUD.Create(ctx, controller, nil)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}

	// The created object should have an etag
	if len(created.CosmosETag) == 0 {
		t.Fatal("Expected created object to have an etag")
	}
	originalETag := created.CosmosETag

	t.Run("positive case - matching etag succeeds", func(t *testing.T) {
		// Get the current version to ensure we have the correct etag
		current, err := controllerCRUD.Get(ctx, controllerName)
		if err != nil {
			t.Fatalf("Failed to get controller: %v", err)
		}

		// Update with the correct etag
		current.Status.Conditions[0].Message = "Updated message"

		replaced, err := controllerCRUD.Replace(ctx, current, nil)
		if err != nil {
			t.Fatalf("Replace with matching etag should succeed, got error: %v", err)
		}

		if replaced.Status.Conditions[0].Message != "Updated message" {
			t.Errorf("Expected condition message to be updated")
		}

		// The new etag should be different from the original
		if replaced.CosmosETag == originalETag {
			t.Errorf("Expected new etag after replace, got same etag")
		}
	})

	t.Run("negative case - mismatched etag fails", func(t *testing.T) {
		// Get the current version
		current, err := controllerCRUD.Get(ctx, controllerName)
		if err != nil {
			t.Fatalf("Failed to get controller: %v", err)
		}

		// Set an incorrect etag
		current.CosmosETag = "wrong-etag-value"

		_, err = controllerCRUD.Replace(ctx, current, nil)
		if err == nil {
			t.Fatal("Replace with wrong etag should fail")
		}

		if !database.IsResponseError(err, 412) {
			t.Errorf("Expected 412 Precondition Failed, got: %v", err)
		}
	})
}

func TestMockLockClient(t *testing.T) {
	ctx := context.Background()
	lockClient := NewMockLockClient(30 * time.Second)

	// Test GetDefaultTimeToLive
	ttl := lockClient.GetDefaultTimeToLive()
	if ttl != 30*time.Second {
		t.Errorf("Expected TTL 30s, got %v", ttl)
	}

	// Test TryAcquireLock
	lock, err := lockClient.TryAcquireLock(ctx, "test-lock")
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}
	if lock == nil {
		t.Fatal("Expected lock to be acquired")
	}

	// Test that same lock can't be acquired again
	lock2, err := lockClient.TryAcquireLock(ctx, "test-lock")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if lock2 != nil {
		t.Error("Expected lock to not be acquired (already held)")
	}

	// Test ReleaseLock
	err = lockClient.ReleaseLock(ctx, lock)
	if err != nil {
		t.Fatalf("Failed to release lock: %v", err)
	}
}
