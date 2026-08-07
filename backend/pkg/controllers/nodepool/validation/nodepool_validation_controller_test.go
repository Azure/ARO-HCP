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

package validation

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

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
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

func newTestCluster(t *testing.T) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newTestNodePool(t *testing.T) *coreapi.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	return &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newTestSubscription() *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		ResourceID: subResourceID,
		State:      coreapi.SubscriptionStateRegistered,
	}
}

// mockNodePoolValidation implements validationutils.NodePoolValidation for tests.
type mockNodePoolValidation struct {
	name        string
	validateErr error
}

var _ validationutils.NodePoolValidation = (*mockNodePoolValidation)(nil)

func (m *mockNodePoolValidation) Name() string { return m.name }

func (m *mockNodePoolValidation) Validate(_ context.Context, _ *coreapi.HCPOpenShiftCluster, _ *coreapi.Subscription, _ *coreapi.HCPOpenShiftClusterNodePool) error {
	return m.validateErr
}

func TestNodePoolValidationSyncer_SyncOnce(t *testing.T) {

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
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
		_, err = corecosmosstorage.GetOrCreateServiceProviderNodePool(ctx, mockDB, nodePool.ID)
		require.NoError(t, err)
	}

	testCases := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		validation          *mockNodePoolValidation
		wantErr             bool
		wantConditionStatus *metav1.ConditionStatus
	}{
		{
			name: "cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
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
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
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
			wantConditionStatus: metadataapi.Ptr(metav1.ConditionTrue),
		},
		{
			name:    "validation fails -- condition set to False and error returned",
			setupDB: defaultSetupDB,
			validation: &mockNodePoolValidation{
				name:        testValidationName,
				validateErr: fmt.Errorf("quota exceeded"),
			},
			wantErr:             true,
			wantConditionStatus: metadataapi.Ptr(metav1.ConditionFalse),
		},
		{
			name: "already-succeeded validation -- skipped",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName)
				spnp, err := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
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

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tc.setupDB != nil {
				tc.setupDB(t, ctx, mockDB)
			}

			syncer := &nodePoolValidationSyncer{
				resourcesDBClient:             mockDB,
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
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
				).Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
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
