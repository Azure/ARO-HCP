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

package operationcontrollers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const testCredentialName = "testcred12345678"

func TestOperationSystemAdminCredential_ShouldProcess(t *testing.T) {
	tests := []struct {
		name      string
		operation *api.Operation
		expected  bool
	}{
		{
			name: "should process RequestCredential with InternalID set",
			operation: &api.Operation{
				Status:     arm.ProvisioningStateAccepted,
				Request:    database.OperationRequestRequestCredential,
				InternalID: api.Must(api.NewInternalID(api.ToSystemAdminCredentialResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))),
			},
			expected: true,
		},
		{
			name: "should not process terminal operation",
			operation: &api.Operation{
				Status:     arm.ProvisioningStateSucceeded,
				Request:    database.OperationRequestRequestCredential,
				InternalID: api.Must(api.NewInternalID(api.ToSystemAdminCredentialResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))),
			},
			expected: false,
		},
		{
			name: "should not process non-credential request",
			operation: &api.Operation{
				Status:     arm.ProvisioningStateAccepted,
				Request:    database.OperationRequestCreate,
				InternalID: api.Must(api.NewInternalID(testClusterServiceIDStr)),
			},
			expected: false,
		},
		{
			name: "should not process when InternalID is empty",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: database.OperationRequestRequestCredential,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &operationSystemAdminCredential{}
			result := controller.ShouldProcess(context.Background(), tt.operation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOperationSystemAdminCredential_SynchronizeOperation(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		credPhase      api.SystemAdminCredentialPhase
		expectedStatus arm.ProvisioningState
		wantErr        bool
	}{
		{
			name:           "Requested phase keeps operation provisioning",
			credPhase:      api.SystemAdminCredentialPhaseRequested,
			expectedStatus: arm.ProvisioningStateProvisioning,
		},
		{
			name:           "Issued phase transitions to succeeded",
			credPhase:      api.SystemAdminCredentialPhaseIssued,
			expectedStatus: arm.ProvisioningStateSucceeded,
		},
		{
			name:           "Failed phase transitions to failed",
			credPhase:      api.SystemAdminCredentialPhaseFailed,
			expectedStatus: arm.ProvisioningStateFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			fixture := newClusterTestFixture()

			cluster := fixture.newCluster(nil)
			op := fixture.newOperation(database.OperationRequestRequestCredential)

			credResourceIDStr := api.ToSystemAdminCredentialResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
			op.InternalID = api.Must(api.NewInternalID(credResourceIDStr))

			credResourceID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))
			cred := &api.SystemAdminCredential{}
			cred.SetResourceID(credResourceID)
			cred.SetPartitionKey(strings.ToLower(testSubscriptionID))
			cred.Spec = api.SystemAdminCredentialSpec{
				Username:            "system-admin",
				OperationID:         testOperationName,
				ExpirationTimestamp: metav1.NewTime(now.Add(24 * time.Hour)),
				PublicKeyPEM:        "test-public-key",
				PrivateKeyPEM:       "test-private-key",
			}
			cred.Status = api.SystemAdminCredentialStatus{
				Phase: tt.credPhase,
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, op, cred})
			require.NoError(t, err)

			controller := &operationSystemAdminCredential{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: mockDB,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			updatedOp, err := mockDB.Operations(testSubscriptionID).Get(ctx, testOperationName)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, updatedOp.Status)
		})
	}
}
