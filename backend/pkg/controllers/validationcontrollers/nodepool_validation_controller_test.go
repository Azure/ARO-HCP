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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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

// mockNodePoolValidation implements validations.NodePoolValidation for tests.
type mockNodePoolValidation struct {
	name   string
	result *validations.ValidationResult
}

var _ validations.NodePoolValidation = (*mockNodePoolValidation)(nil)

func (m *mockNodePoolValidation) Name() string { return m.name }

func (m *mockNodePoolValidation) Validate(_ context.Context, _ *api.HCPOpenShiftCluster, _ *arm.Subscription, _ *api.HCPOpenShiftClusterNodePool) *validations.ValidationResult {
	return m.result
}

// alwaysSyncCooldownChecker always allows syncing.
type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool { return true }

var _ controllerutil.CooldownChecker = (*alwaysSyncCooldownChecker)(nil)

// mockAfterEnqueuer records EnqueueAfter calls.
type mockAfterEnqueuer struct {
	calls []enqueueAfterCall
}

type enqueueAfterCall struct {
	key      any
	duration time.Duration
}

func (m *mockAfterEnqueuer) EnqueueAfter(keyObj any, duration time.Duration) {
	m.calls = append(m.calls, enqueueAfterCall{key: keyObj, duration: duration})
}

func TestNodePoolValidationSyncer_SyncOnce(t *testing.T) {

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, newTestNodePool(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
	}

	testCases := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		validation          *mockNodePoolValidation
		wantEnqueueCount    int
		wantConditionStatus *metav1.ConditionStatus
		wantConditionReason string
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
				name:   testValidationName,
				result: &validations.ValidationResult{Outcome: validations.OutcomeTypePassed},
			},
			wantConditionStatus: api.Ptr(metav1.ConditionTrue),
			wantConditionReason: "AsExpected",
		},
		{
			name:    "validation fails -- condition set to False and enqueued for retry",
			setupDB: defaultSetupDB,
			validation: &mockNodePoolValidation{
				name: testValidationName,
				result: &validations.ValidationResult{
					Outcome: validations.OutcomeTypeFailed,
					Failed: &validations.FailedResult{
						Reason:                 "QuotaExceeded",
						ServiceProviderMessage: "quota exceeded",
						UserMessage:            "Quota exceeded.",
					},
					EarliestRetryAfter: ptr.To(60 * time.Second),
				},
			},
			wantEnqueueCount:    1,
			wantConditionStatus: api.Ptr(metav1.ConditionFalse),
			wantConditionReason: "QuotaExceeded",
		},
		{
			name:    "nil validation result -- treated as Unknown and enqueued for retry",
			setupDB: defaultSetupDB,
			validation: &mockNodePoolValidation{
				name:   testValidationName,
				result: nil,
			},
			wantEnqueueCount:    1,
			wantConditionStatus: api.Ptr(metav1.ConditionUnknown),
			wantConditionReason: "NilResult",
		},
		{
			name:    "unknown with LogOnly -- condition set to Unknown, enqueued for retry",
			setupDB: defaultSetupDB,
			validation: &mockNodePoolValidation{
				name: testValidationName,
				result: &validations.ValidationResult{
					Outcome: validations.OutcomeTypeUnknown,
					Unknown: &validations.UnknownResult{
						Reason:                 "Transient",
						ServiceProviderMessage: "transient issue",
						UserMessage:            "Validation status is unknown.",
						ReportingPolicy:        validations.ReportingPolicyTypeLogOnly,
					},
					EarliestRetryAfter: ptr.To(60 * time.Second),
				},
			},
			wantEnqueueCount:    1,
			wantConditionStatus: api.Ptr(metav1.ConditionUnknown),
			wantConditionReason: "Transient",
		},
		{
			name: "already-succeeded validation -- skipped",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spnpResourceID := api.Must(azcorearm.ParseResourceID(
					"/subscriptions/" + testSubscriptionID +
						"/resourceGroups/" + testResourceGroup +
						"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
						"/nodePools/" + testNodePoolName +
						"/serviceProviderNodePools/default"))
				spnp := &api.ServiceProviderNodePool{
					CosmosMetadata: arm.CosmosMetadata{
						ResourceID:   spnpResourceID,
						PartitionKey: strings.ToLower(spnpResourceID.SubscriptionID),
					},
					Status: api.ServiceProviderNodePoolStatus{
						Validations: []metav1.Condition{
							{
								Type:   testValidationName,
								Status: metav1.ConditionTrue,
								Reason: "Succeeded",
							},
						},
					},
				}
				_, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName).Create(ctx, spnp, nil)
				require.NoError(t, err)
			},
			validation: &mockNodePoolValidation{
				name: testValidationName,
				result: &validations.ValidationResult{
					Outcome: validations.OutcomeTypeFailed,
					Failed: &validations.FailedResult{
						Reason:                 "ShouldNotBeCalled",
						ServiceProviderMessage: "should not be called",
						UserMessage:            "should not be called",
					},
				},
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

			mockEnqueuer := &mockAfterEnqueuer{}
			syncer := &nodePoolValidationSyncer{
				cooldownChecker:          &alwaysSyncCooldownChecker{},
				retryCooldownChecker:     controllerutil.NewSettableCooldownChecker(),
				enqueueAfter:             mockEnqueuer,
				resourcesDBClient:        mockDB,
				consecutiveUnknownCounts: lru.New(100),
				validation:               tc.validation,
			}

			err := syncer.SyncOnce(ctx, newTestNodePoolKey())
			require.NoError(t, err)

			assert.Len(t, mockEnqueuer.calls, tc.wantEnqueueCount)

			if tc.wantConditionStatus != nil {
				spnp, spnpErr := mockDB.ServiceProviderNodePools(
					testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName,
				).Get(ctx, "default")
				require.NoError(t, spnpErr)

				cond := meta.FindStatusCondition(spnp.Status.Validations, testValidationName)
				require.NotNil(t, cond, "expected validation condition to be set")
				assert.Equal(t, *tc.wantConditionStatus, cond.Status)

				if tc.wantConditionReason != "" {
					assert.Equal(t, tc.wantConditionReason, cond.Reason)
				}
			}
		})
	}
}

func TestNodePoolValidationSyncer_EnqueueAfterTiming(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := databasetesting.NewMockResourcesDBClient()
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
	require.NoError(t, err)
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroup).NodePools(testClusterName).Create(ctx, newTestNodePool(t), nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)

	mockEnqueuer := &mockAfterEnqueuer{}
	syncer := &nodePoolValidationSyncer{
		cooldownChecker:          &alwaysSyncCooldownChecker{},
		retryCooldownChecker:     controllerutil.NewSettableCooldownChecker(),
		enqueueAfter:             mockEnqueuer,
		resourcesDBClient:        mockDB,
		consecutiveUnknownCounts: lru.New(100),
		validation: &mockNodePoolValidation{
			name: testValidationName,
			result: &validations.ValidationResult{
				Outcome: validations.OutcomeTypeFailed,
				Failed: &validations.FailedResult{
					Reason:                 "QuotaExceeded",
					ServiceProviderMessage: "quota exceeded",
					UserMessage:            "Quota exceeded.",
				},
				EarliestRetryAfter: ptr.To(120 * time.Second),
			},
		},
	}

	err = syncer.SyncOnce(ctx, newTestNodePoolKey())
	require.NoError(t, err)
	require.Len(t, mockEnqueuer.calls, 1)
	assert.Equal(t, 121*time.Second, mockEnqueuer.calls[0].duration)
}

func TestClusterValidationSyncer_SyncOnce(t *testing.T) {
	newTestClusterKey := func() controllerutils.HCPClusterKey {
		return controllerutils.HCPClusterKey{
			SubscriptionID:    testSubscriptionID,
			ResourceGroupName: testResourceGroup,
			HCPClusterName:    testClusterName,
		}
	}

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
	}

	testCases := []struct {
		name                string
		setupDB             func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		result              *validations.ValidationResult
		wantEnqueueCount    int
		wantConditionStatus *metav1.ConditionStatus
		wantConditionReason string
	}{
		{
			name: "cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
		},
		{
			name:                "validation succeeds -- condition set to True",
			setupDB:             defaultSetupDB,
			result:              &validations.ValidationResult{Outcome: validations.OutcomeTypePassed},
			wantConditionStatus: api.Ptr(metav1.ConditionTrue),
			wantConditionReason: "AsExpected",
		},
		{
			name:    "validation fails -- condition set to False, enqueued for retry",
			setupDB: defaultSetupDB,
			result: &validations.ValidationResult{
				Outcome: validations.OutcomeTypeFailed,
				Failed: &validations.FailedResult{
					Reason:                 "QuotaExceeded",
					ServiceProviderMessage: "quota exceeded",
					UserMessage:            "Quota exceeded.",
				},
				EarliestRetryAfter: ptr.To(60 * time.Second),
			},
			wantEnqueueCount:    1,
			wantConditionStatus: api.Ptr(metav1.ConditionFalse),
			wantConditionReason: "QuotaExceeded",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockDB := databasetesting.NewMockResourcesDBClient()
			if tc.setupDB != nil {
				tc.setupDB(t, ctx, mockDB)
			}

			mockEnqueuer := &mockAfterEnqueuer{}
			syncer := &clusterValidationSyncer{
				cooldownChecker:          &alwaysSyncCooldownChecker{},
				retryCooldownChecker:     controllerutil.NewSettableCooldownChecker(),
				enqueueAfter:             mockEnqueuer,
				resourcesDBClient:        mockDB,
				consecutiveUnknownCounts: lru.New(100),
				validation: &mockClusterValidation{
					name:   testValidationName,
					result: tc.result,
				},
			}

			err := syncer.SyncOnce(ctx, newTestClusterKey())
			require.NoError(t, err)

			assert.Len(t, mockEnqueuer.calls, tc.wantEnqueueCount)

			if tc.wantConditionStatus != nil {
				spc, spcErr := mockDB.ServiceProviderClusters(
					testSubscriptionID, testResourceGroup, testClusterName,
				).Get(ctx, "default")
				require.NoError(t, spcErr)

				cond := meta.FindStatusCondition(spc.Status.Validations, testValidationName)
				require.NotNil(t, cond, "expected validation condition to be set")
				assert.Equal(t, *tc.wantConditionStatus, cond.Status)

				if tc.wantConditionReason != "" {
					assert.Equal(t, tc.wantConditionReason, cond.Reason)
				}
			}
		})
	}
}

// mockClusterValidation implements validations.ClusterValidation for tests.
type mockClusterValidation struct {
	name   string
	result *validations.ValidationResult
}

var _ validations.ClusterValidation = (*mockClusterValidation)(nil)

func (m *mockClusterValidation) Name() string { return m.name }

func (m *mockClusterValidation) Validate(_ context.Context, _ *arm.Subscription, _ *api.HCPOpenShiftCluster) *validations.ValidationResult {
	return m.result
}
