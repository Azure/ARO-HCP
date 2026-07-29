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

package operationcontrollers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationExternalAuthUpdate_SynchronizeOperation(t *testing.T) {
	testClockNow := time.Now()
	fixture := newExternalAuthTestFixture()

	newExternalAuth := func(mutate ...func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
		externalAuth := fixture.newExternalAuth()
		for _, fn := range mutate {
			if fn != nil {
				fn(externalAuth)
			}
		}
		return externalAuth
	}

	newOperationAccepted := func() *api.Operation {
		return fixture.newOperation(database.OperationRequestUpdate)
	}

	preconditionExistingOperation := fixture.newOperation(database.OperationRequestUpdate)
	preconditionListerOperation := fixture.newOperation(database.OperationRequestUpdate)
	preconditionListerOperation.CosmosETag = "stale-etag"
	// Not seeded to Cosmos, so PrepareForCreate never runs. UpdateOperationStatus still
	// requires a non-zero InstanceVersion before it will attempt the etag-checked replace.
	preconditionListerOperation.InstanceVersion = 1

	newPassingCachedHostedClusterReadDesire := func() *kubeapplier.ReadDesire {
		return newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
			Spec: testExternalAuthUpdateMatchingHostedClusterSpec(),
		})
	}

	newPassingCSExternalAuth := func(t *testing.T, externalAuth *api.HCPOpenShiftClusterExternalAuth) *arohcpv1alpha1.ExternalAuth {
		t.Helper()
		builder, err := ocm.BuildCSExternalAuth(context.Background(), externalAuth, true)
		require.NoError(t, err)
		csExternalAuth, err := builder.Build()
		require.NoError(t, err)
		return csExternalAuth
	}

	newPassingExternalAuth := func(mutate ...func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
		return newExternalAuthUpdateTestExternalAuth(mutate...)
	}

	verifyOperationAndExternalAuthUpdating := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
		require.NoError(t, err)
		assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)
		assert.Nil(t, op.Error)

		externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
		require.NoError(t, err)
		assert.Equal(t, arm.ProvisioningStateUpdating, externalAuth.Properties.ProvisioningState)
		assert.Equal(t, testOperationName, externalAuth.ServiceProviderProperties.ActiveOperationID)
	}

	testCases := []struct {
		name                          string
		existingExternalAuth          *api.HCPOpenShiftClusterExternalAuth
		existingOperation             *api.Operation
		externalAuthLister            listers.ExternalAuthLister
		activeOperationsLister        listers.ActiveOperationLister
		cachedHostedClusterReadDesire *kubeapplier.ReadDesire
		setupMockCSClient             func(*ocm.MockClusterServiceClientSpec)
		wantErr                       bool
		verifyDB                      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:                          "external auth update succeeds when CS and hypershift specs match desired",
			existingExternalAuth:          newPassingExternalAuth(),
			existingOperation:             newOperationAccepted(),
			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				passingExternalAuth := newPassingExternalAuth()
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(newPassingCSExternalAuth(t, passingExternalAuth), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, externalAuth.Properties.ProvisioningState)
				assert.Empty(t, externalAuth.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                          "cs external auth spec mismatch keeps operation updating",
			existingExternalAuth:          newPassingExternalAuth(),
			existingOperation:             newOperationAccepted(),
			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				staleCSExternalAuth := newPassingCSExternalAuth(t, newPassingExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
					ea.Properties.Issuer.URL = "https://stale.example.com"
				}))
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(staleCSExternalAuth, nil)
			},
			verifyDB: verifyOperationAndExternalAuthUpdating,
		},
		{
			name:                 "hypershift not observed keeps operation updating",
			existingExternalAuth: newPassingExternalAuth(),
			existingOperation:    newOperationAccepted(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				passingExternalAuth := newPassingExternalAuth()
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(newPassingCSExternalAuth(t, passingExternalAuth), nil)
			},
			verifyDB: verifyOperationAndExternalAuthUpdating,
		},
		{
			name:                 "hypershift issuer spec mismatch keeps operation updating",
			existingExternalAuth: newPassingExternalAuth(),
			existingOperation:    newOperationAccepted(),
			cachedHostedClusterReadDesire: newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
				Spec: func() v1beta1.HostedClusterSpec {
					spec := testExternalAuthUpdateMatchingHostedClusterSpec()
					provider := testExternalAuthUpdateMatchingOIDCProvider()
					provider.Issuer.URL = "https://stale.example.com"
					spec.Configuration.Authentication.OIDCProviders = []configv1.OIDCProvider{provider}
					return spec
				}(),
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				passingExternalAuth := newPassingExternalAuth()
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(newPassingCSExternalAuth(t, passingExternalAuth), nil)
			},
			verifyDB: verifyOperationAndExternalAuthUpdating,
		},
		{
			name:                          "cs external auth get error returns error",
			existingExternalAuth:          newExternalAuth(),
			existingOperation:             newOperationAccepted(),
			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, fmt.Errorf("cluster service error"))
			},
			wantErr: true,
		},
		{
			name:                 "external auth not in lister cache leaves operation unchanged",
			existingExternalAuth: newExternalAuth(),
			existingOperation:    newOperationAccepted(),
			externalAuthLister:   &listertesting.SliceExternalAuthLister{},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, externalAuth.ServiceProviderProperties.ActiveOperationID)
				assert.Equal(t, arm.ProvisioningStateAccepted, externalAuth.Properties.ProvisioningState)
			},
		},
		{
			name: "active operation id mismatch leaves operation unchanged",
			existingExternalAuth: newExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.ActiveOperationID = "other-operation"
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when ClusterServiceID is nil",
			existingExternalAuth: newExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when external auth is deleting",
			existingExternalAuth: newExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: testClockNow}
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                          "precondition failed on status update is ignored",
			existingExternalAuth:          newPassingExternalAuth(),
			existingOperation:             preconditionExistingOperation,
			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			activeOperationsLister: &listertesting.SliceActiveOperationLister{
				Operations: []*api.Operation{preconditionListerOperation},
			},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				passingExternalAuth := newPassingExternalAuth()
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(newPassingCSExternalAuth(t, passingExternalAuth), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status, "operation should be unchanged after optimistic concurrency conflict")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{fixture.newCluster()}
			if tc.existingExternalAuth != nil {
				resources = append(resources, tc.existingExternalAuth)
			}
			if tc.existingOperation != nil {
				resources = append(resources, tc.existingOperation)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var readDesireLister internallistertesting.SliceReadDesireLister
			if tc.cachedHostedClusterReadDesire != nil {
				readDesireLister = internallistertesting.SliceReadDesireLister{
					Desires: []*kubeapplier.ReadDesire{tc.cachedHostedClusterReadDesire},
				}
			}

			externalAuthLister := tc.externalAuthLister
			if externalAuthLister == nil {
				externalAuthLister = &listertesting.DBExternalAuthLister{ResourcesDBClient: mockResourcesDBClient}
			}
			activeOperationsLister := tc.activeOperationsLister
			if activeOperationsLister == nil {
				activeOperationsLister = &listertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			controller := &operationExternalAuthUpdate{
				clock:                  utilsclock.RealClock{},
				resourcesDBClient:      mockResourcesDBClient,
				clusterServiceClient:   mockCSClient,
				externalAuthLister:     externalAuthLister,
				readDesireLister:       &readDesireLister,
				activeOperationsLister: activeOperationsLister,
				notificationClient:     nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
