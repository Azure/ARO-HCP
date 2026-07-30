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

package legacycredentialrequest

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRequestCredential_ShouldProcess(t *testing.T) {
	tests := []struct {
		name              string
		operationOverride func(*coreapi.Operation)
		expectedResult    bool
	}{
		{
			name:              "Accepted status should be processed",
			operationOverride: func(o *coreapi.Operation) { o.Status = coreapi.ProvisioningStateAccepted },
			expectedResult:    true,
		},
		{
			name:              "Terminal ProvisioningState should not be processed",
			operationOverride: func(o *coreapi.Operation) { o.Status = coreapi.ProvisioningStateSucceeded },
			expectedResult:    false,
		},
		{
			name: "Wrong operation request type should not be processed",
			operationOverride: func(o *coreapi.Operation) {
				o.Request = cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation
			},
			expectedResult: false,
		},
		{
			name: "SystemAdminCredentialRequest set should not be processed",
			operationOverride: func(o *coreapi.Operation) {
				o.SystemAdminCredentialRequest = &coreapi.OperationSystemAdminCredentialRequest{
					CertificateSigningRequest: "-----BEGIN CERTIFICATE REQUEST-----\ntest\n-----END CERTIFICATE REQUEST-----",
				}
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			fixture := operationtesting.NewClusterTestFixture()
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestSystemAdminCredentialRequest)
			operation.Status = coreapi.ProvisioningStateAccepted
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			controller := &operationRequestCredential{}
			result := controller.ShouldProcess(ctx, operation)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestOperationRequestCredential_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name                       string
		operationOverride          func(*coreapi.Operation)
		breakGlassCredentialStatus cmv1.BreakGlassCredentialStatus
		getBreakGlassCredentialErr error
		expectError                bool
		expectCSMockCalled         bool
		verify                     func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture)
	}{
		{
			name:                       "created credential updates operation status to provisioning",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusCreated,
			expectError:                false,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:                       "failed credential updates operation status to failed",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusFailed,
			expectError:                false,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.Equal(t, coreapi.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:                       "issued credential updates operation status to succeeded",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusIssued,
			expectError:                false,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:                       "unhandled BreakGlassCredentialStatus leads to error",
			breakGlassCredentialStatus: "CompleteFantasy",
			expectError:                true,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status) // no state change
			},
		},
		{
			name:                       "GetBreakGlassCredential failure leads to error",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusIssued,
			getBreakGlassCredentialErr: errors.New("something went wrong"),
			expectError:                true,
			expectCSMockCalled:         true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status) // no state change
			},
		},
		{
			name:               "ShouldProcess returns false for terminal status and no state change occurs",
			operationOverride:  func(o *coreapi.Operation) { o.Status = coreapi.ProvisioningStateSucceeded },
			expectError:        false,
			expectCSMockCalled: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status) // no state change
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := operationtesting.NewClusterTestFixture()
			cluster := fixture.NewCluster(nil)
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestSystemAdminCredentialRequest)
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tt.expectCSMockCalled {
				breakGlassCredential, err := cmv1.NewBreakGlassCredential().
					Status(tt.breakGlassCredentialStatus).
					Build()
				require.NoError(t, err)

				mockCSClient.EXPECT().
					GetBreakGlassCredential(gomock.Any(), fixture.ClusterInternalID).
					Return(breakGlassCredential, tt.getBreakGlassCredentialErr)
			}

			controller := &operationRequestCredential{
				clock:                 utilsclock.RealClock{},
				resourcesDBClient:     mockResourcesDBClient,
				clustersServiceClient: mockCSClient,
			}

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
