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

package oldoperationscanner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestSetDeleteOperationAsCompleted(t *testing.T) {
	tests := []struct {
		name                    string
		operationStatus         arm.ProvisioningState
		resourceDocPresent      bool
		expectAsyncNotification bool
		expectError             bool
	}{
		{
			name:                    "Database updated properly",
			operationStatus:         arm.ProvisioningStateDeleting,
			resourceDocPresent:      true,
			expectAsyncNotification: true,
			expectError:             false,
		},
		{
			name:                    "Resource already deleted",
			operationStatus:         arm.ProvisioningStateDeleting,
			resourceDocPresent:      false,
			expectAsyncNotification: true,
			expectError:             false,
		},
		{
			name:                    "Operation already succeeded",
			operationStatus:         arm.ProvisioningStateSucceeded,
			resourceDocPresent:      true,
			expectAsyncNotification: true,
			expectError:             false,
		},
	}

	// Placeholder InternalID for NewOperation
	internalID, err := api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/placeholder")
	require.NoError(t, err)

	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)

	operationID, err := azcorearm.ParseResourceID(api.TestSubscriptionResourceID + "/providers/" + api.ProviderNamespace + "/locations/oz/" + api.OperationStatusResourceTypeName + "/operationID")
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request

			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			// Use databasetesting mock instead of gomock
			mockDBClient := databasetesting.NewMockDBClient()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					request = r
				}
			}))
			defer server.Close()

			scanner := &OperationsScanner{
				dbClient:           mockDBClient,
				notificationClient: server.Client(),
				newTimestamp:       func() time.Time { return time.Now().UTC() },
			}

			operationDoc := database.NewOperation(
				database.OperationRequestDelete,
				resourceID,
				internalID,
				"azure-location",
				"",
				"",
				"",
				nil)
			operationDoc.OperationID = operationID
			// Update the CosmosMetadata.ResourceID to match the operationID (without location in path)
			// This is required because CosmosMetadata.ResourceID is used for cosmos storage key
			cosmosResourceID, err := azcorearm.ParseResourceID(path.Join("/",
				"subscriptions", operationID.SubscriptionID,
				"providers", api.ProviderNamespace,
				api.OperationStatusResourceTypeName, operationID.Name,
			))
			require.NoError(t, err)
			operationDoc.CosmosMetadata.ResourceID = cosmosResourceID
			operationDoc.ResourceID = cosmosResourceID
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.operationStatus

			// Store the operation in the database
			_, err = mockDBClient.Operations(operationID.SubscriptionID).Create(ctx, operationDoc, nil)
			require.NoError(t, err)

			// If resource should be present, create a cluster document
			if tt.resourceDocPresent {
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{ID: resourceID},
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: internalID,
					},
				}
				_, err := mockDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
			}

			err = operationcontrollers.SetDeleteOperationAsCompleted(ctx, scanner.dbClient, operationDoc, scanner.postAsyncNotification)

			if tt.expectError {
				assert.Error(t, err)

			} else if assert.NoError(t, err) {
				// Verify the operation status was updated to Succeeded
				updatedOp, err := mockDBClient.Operations(operationID.SubscriptionID).Get(ctx, operationID.Name)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, updatedOp.Status)

				// If resource was present, verify it was deleted
				if tt.resourceDocPresent {
					_, err := mockDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
					assert.Error(t, err, "Resource should have been deleted")
				}

				if tt.expectAsyncNotification {
					assert.NotNil(t, request, "Did not POST to async notification URI")
					assert.Empty(t, updatedOp.NotificationURI)
				} else {
					assert.Nil(t, request, "Unexpected POST to async notification URI")
				}
			}
		})
	}
}

func TestUpdateOperationStatus(t *testing.T) {
	tests := []struct {
		name                             string
		currentOperationStatus           arm.ProvisioningState
		updatedOperationStatus           arm.ProvisioningState
		resourceDocPresent               bool
		resourceMatchOperationID         bool
		resourceProvisioningState        arm.ProvisioningState
		expectAsyncNotification          bool
		expectResourceOperationIDCleared bool
		expectResourceProvisioningState  arm.ProvisioningState
		expectError                      bool
	}{
		{
			name:                             "Resource updated to terminal state",
			currentOperationStatus:           arm.ProvisioningStateProvisioning,
			updatedOperationStatus:           arm.ProvisioningStateSucceeded,
			resourceDocPresent:               true,
			resourceMatchOperationID:         true,
			resourceProvisioningState:        arm.ProvisioningStateProvisioning,
			expectAsyncNotification:          true,
			expectResourceOperationIDCleared: true,
			expectResourceProvisioningState:  arm.ProvisioningStateSucceeded,
			expectError:                      false,
		},
		{
			name:                             "Resource updated to non-terminal state",
			currentOperationStatus:           arm.ProvisioningStateSucceeded,
			updatedOperationStatus:           arm.ProvisioningStateDeleting,
			resourceDocPresent:               true,
			resourceMatchOperationID:         true,
			resourceProvisioningState:        arm.ProvisioningStateSucceeded,
			expectAsyncNotification:          false,
			expectResourceOperationIDCleared: false,
			expectResourceProvisioningState:  arm.ProvisioningStateDeleting,
			expectError:                      false,
		},
		{
			name:                             "Operation already at target provisioning state",
			currentOperationStatus:           arm.ProvisioningStateSucceeded,
			updatedOperationStatus:           arm.ProvisioningStateSucceeded,
			resourceDocPresent:               true,
			resourceMatchOperationID:         true,
			resourceProvisioningState:        arm.ProvisioningStateSucceeded,
			expectAsyncNotification:          true,
			expectResourceOperationIDCleared: true,
			expectResourceProvisioningState:  arm.ProvisioningStateSucceeded,
			expectError:                      false,
		},
		{
			name:                    "Resource not found",
			currentOperationStatus:  arm.ProvisioningStateProvisioning,
			updatedOperationStatus:  arm.ProvisioningStateSucceeded,
			resourceDocPresent:      false,
			expectAsyncNotification: true,
			expectError:             false,
		},
		{
			name:                             "Resource has a different active operation",
			currentOperationStatus:           arm.ProvisioningStateProvisioning,
			updatedOperationStatus:           arm.ProvisioningStateSucceeded,
			resourceDocPresent:               true,
			resourceMatchOperationID:         false,
			resourceProvisioningState:        arm.ProvisioningStateDeleting,
			expectAsyncNotification:          true,
			expectResourceOperationIDCleared: false,
			expectResourceProvisioningState:  arm.ProvisioningStateDeleting,
			expectError:                      false,
		},
	}

	// Placeholder InternalID for NewOperation
	internalID, err := api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/placeholder")
	require.NoError(t, err)

	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)

	operationID, err := azcorearm.ParseResourceID(api.TestSubscriptionResourceID + "/providers/" + api.ProviderNamespace + "/locations/oz/" + api.OperationStatusResourceTypeName + "/operationID")
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request

			ctx := context.Background()

			// Use databasetesting mock instead of gomock
			mockDBClient := databasetesting.NewMockDBClient()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					request = r
				}
			}))
			defer server.Close()

			scanner := &OperationsScanner{
				notificationClient: server.Client(),
				newTimestamp:       func() time.Time { return time.Now().UTC() },
			}

			operationDoc := database.NewOperation(
				database.OperationRequestCreate,
				resourceID,
				internalID,
				"azure-location",
				"",
				"",
				"",
				nil)
			operationDoc.OperationID = operationID
			// Update the CosmosMetadata.ResourceID to match the operationID (without location in path)
			// This is required because CosmosMetadata.ResourceID is used for cosmos storage key
			cosmosResourceID, err := azcorearm.ParseResourceID(path.Join("/",
				"subscriptions", operationID.SubscriptionID,
				"providers", api.ProviderNamespace,
				api.OperationStatusResourceTypeName, operationID.Name,
			))
			require.NoError(t, err)
			operationDoc.CosmosMetadata.ResourceID = cosmosResourceID
			operationDoc.ResourceID = cosmosResourceID
			operationDoc.NotificationURI = server.URL
			operationDoc.Status = tt.currentOperationStatus

			// Store the operation in the database
			_, err = mockDBClient.Operations(operationID.SubscriptionID).Create(ctx, operationDoc, nil)
			require.NoError(t, err)

			// If resource should be present, create a cluster document
			if tt.resourceDocPresent {
				resourceDoc := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID: resourceID,
						},
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ProvisioningState: tt.resourceProvisioningState,
						ClusterServiceID:  internalID,
					},
				}
				if tt.resourceMatchOperationID {
					resourceDoc.ServiceProviderProperties.ActiveOperationID = operationDoc.OperationID.Name
				} else {
					resourceDoc.ServiceProviderProperties.ActiveOperationID = "another operation"
				}
				_, err := mockDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, resourceDoc, nil)
				require.NoError(t, err)
			}

			err = operationcontrollers.UpdateOperationStatus(ctx, mockDBClient, operationDoc, tt.updatedOperationStatus, nil, scanner.postAsyncNotification)

			if tt.expectError {
				assert.Error(t, err)

			} else if assert.NoError(t, err) {
				// Verify operation status was updated
				updatedOp, err := mockDBClient.Operations(operationID.SubscriptionID).Get(ctx, operationID.Name)
				require.NoError(t, err)
				assert.Equal(t, tt.updatedOperationStatus, updatedOp.Status)

				if tt.resourceDocPresent {
					// Verify resource state
					updatedResource, err := mockDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
					require.NoError(t, err)

					if tt.expectResourceOperationIDCleared {
						assert.Empty(t, updatedResource.ServiceProviderProperties.ActiveOperationID, "Resource's active operation ID was not cleared")
					} else {
						assert.NotEmpty(t, updatedResource.ServiceProviderProperties.ActiveOperationID, "Resource's active operation ID is unexpectedly empty")
					}

					assert.Equal(t, tt.expectResourceProvisioningState, updatedResource.ServiceProviderProperties.ProvisioningState)
				}

				if tt.expectAsyncNotification {
					assert.NotNil(t, request, "Did not POST to async notification URI")
					// Verify notification URI was cleared
					updatedOp2, err := mockDBClient.Operations(operationID.SubscriptionID).Get(ctx, operationID.Name)
					require.NoError(t, err)
					assert.Empty(t, updatedOp2.NotificationURI)
				} else {
					assert.Nil(t, request, "Unexpected POST to async notification URI")
				}
			}
		})
	}
}
