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

package validationcontrollers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testResourceGroup  = "test-rg"
	testClusterName    = "test-cluster"
	testNodePoolName   = "test-nodepool"
	testValidationName = "TestValidation"
)

func newTestNodePoolKey() controllerutils.HCPNodePoolKey {
	return controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}
}

func newTestCluster(t *testing.T) *api.HCPOpenShiftCluster {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newTestNodePool(t *testing.T) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	return &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newTestSubscription() *arm.Subscription {
	subResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &arm.Subscription{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		ResourceID: subResourceID,
		State:      arm.SubscriptionStateRegistered,
	}
}

type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

var _ controllerutil.CooldownChecker = &alwaysSyncCooldownChecker{}

// mockNodePoolValidation implements validations.NodePoolValidation for tests.
type mockNodePoolValidation struct {
	name        string
	validateErr error
}

var _ validations.NodePoolValidation = (*mockNodePoolValidation)(nil)

func (m *mockNodePoolValidation) Name() string { return m.name }

func (m *mockNodePoolValidation) Validate(_ context.Context, _ *api.HCPOpenShiftCluster, _ *arm.Subscription, _ *api.HCPOpenShiftClusterNodePool) error {
	return m.validateErr
}

func TestNodePoolValidationSyncer_SyncOnce(t *testing.T) {

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		nodePool := newTestNodePool(t)
		_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, nodePool, nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		// Seed an empty ServiceProviderNodePool the way the production creator
		// controller would have populated it by the time the syncer runs.
		_, err = database.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePool.ID)
		require.NoError(t, err)
	}

	testCases := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		validation          *mockNodePoolValidation
		wantErr             bool
		wantConditionStatus *metav1.ConditionStatus
	}{
		{
			name: "cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, newTestNodePool(t), nil)
				require.NoError(t, err)
				_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: &mockNodePoolValidation{name: testValidationName},
		},
		{
			name: "node pool not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
				require.NoError(t, err)
				_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: &mockNodePoolValidation{name: testValidationName},
		},
		{
			name:    "validation succeeds -- condition set to True",
			setupDB: defaultSetupDB,
			validation: &mockNodePoolValidation{
				name: testValidationName,
			},
			wantConditionStatus: api.Ptr(metav1.ConditionTrue),
		},
		{
			name:    "validation fails -- condition set to False and error returned",
			setupDB: defaultSetupDB,
			validation: &mockNodePoolValidation{
				name:        testValidationName,
				validateErr: fmt.Errorf("quota exceeded"),
			},
			wantErr:             true,
			wantConditionStatus: api.Ptr(metav1.ConditionFalse),
		},
		{
			name: "already-succeeded validation -- skipped",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName)
				spnp, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				spnp.Status.Validations = []metav1.Condition{
					{
						Type:   testValidationName,
						Status: metav1.ConditionTrue,
						Reason: "Succeeded",
					},
				}
				_, err = spnpCRUD.Replace(ctx, spnp, nil)
				require.NoError(t, err)
			},
			validation: &mockNodePoolValidation{
				name:        testValidationName,
				validateErr: fmt.Errorf("should not be called"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockDB := databasetesting.NewMockResourcesDBClient()
			if tc.setupDB != nil {
				tc.setupDB(t, ctx, mockDB)
			}

			syncer := &nodePoolValidationSyncer{
				cooldownChecker:               &alwaysSyncCooldownChecker{},
				resourcesDBClient:             mockDB,
				serviceProviderNodePoolLister: &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
				validation:                    tc.validation,
			}

			err := syncer.SyncOnce(ctx, newTestNodePoolKey())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.wantConditionStatus != nil {
				spnp, spnpErr := mockDB.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName,
				).Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, spnpErr)

				cond := meta.FindStatusCondition(spnp.Status.Validations, testValidationName)
				require.NotNil(t, cond, "expected validation condition to be set")
				assert.Equal(t, *tc.wantConditionStatus, cond.Status)

				if tc.validation.validateErr != nil {
					assert.Equal(t, "Failed", cond.Reason)
					assert.Contains(t, cond.Message, tc.validation.validateErr.Error())
				} else {
					assert.Equal(t, "Succeeded", cond.Reason)
				}
			}
		})
	}
}
