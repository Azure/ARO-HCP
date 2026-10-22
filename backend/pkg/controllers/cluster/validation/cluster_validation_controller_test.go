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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testResourceGroup  = "test-rg"
	testClusterName    = "test-cluster"
	testValidationName = "TestValidation"
)

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type fakeAfterEnqueuer struct {
	enqueuedKeys      []any
	enqueuedDurations []time.Duration
}

func (f *fakeAfterEnqueuer) EnqueueAfter(keyObj any, duration time.Duration) {
	f.enqueuedKeys = append(f.enqueuedKeys, keyObj)
	f.enqueuedDurations = append(f.enqueuedDurations, duration)
}

func newTestClusterKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    testClusterName,
	}
}

func newTestCluster(t *testing.T) *coreapi.Cluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	return &coreapi.Cluster{
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

func newTestSubscription() *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		State: coreapi.SubscriptionStateRegistered,
	}
}

func newTestSyncer(mockDB *corecosmosstoragetesting.MockResourcesDBClient, validation validationutils.ClusterValidation, fakeClock *clocktesting.FakePassiveClock) (*clusterValidationSyncer, *fakeAfterEnqueuer) {
	retryCooldown := controllerutil.NewSettableCooldownChecker()
	retryCooldown.SetClock(fakeClock)
	enqueuer := &fakeAfterEnqueuer{}
	syncer := &clusterValidationSyncer{
		retryCooldownChecker:         retryCooldown,
		enqueueAfter:                 enqueuer,
		resourcesDBClient:            mockDB,
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		validation:                   validation,
		consecutiveUnknownCounts:     lru.New(consecutiveUnknownCountsCacheCapacity),
		probeWriteTimes:              lru.New(consecutiveUnknownCountsCacheCapacity),
		clock:                        fakeClock,
	}
	return syncer, enqueuer
}

func TestClusterValidationSyncer_SyncOnce(t *testing.T) {

	defaultSetupDB := func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		cluster := newTestCluster(t)
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
		require.NoError(t, err)
	}

	testCases := []struct {
		name       string
		setupDB    func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		validation validationutils.ClusterValidation
		wantErr    bool
		// wantCondition, if non-nil, asserts that the stored validation condition's Status/Reason/Message
		// match (Type and LastTransitionTime are not compared).
		wantCondition *metav1.Condition
		// wantConditionAbsent asserts that no validation condition is stored at all.
		wantConditionAbsent bool
		wantEnqueue         bool
	}{
		{
			name: "cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName),
		},
		{
			name: "service provider cluster not found -- no-op",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
				require.NoError(t, err)
				_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName),
		},
		{
			name:          "validation passes -- condition set to True, no requeue",
			setupDB:       defaultSetupDB,
			validation:    NewMockClusterValidation(testValidationName).WithPassed(),
			wantCondition: &metav1.Condition{Status: metav1.ConditionTrue, Reason: "AsExpected", Message: "As expected."},
			wantEnqueue:   false,
		},
		{
			name:    "validation fails -- condition set to False, requeue scheduled",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   true,
		},
		{
			// Covers the "last step of SyncOnce" reporting-policy branch that turns an Unknown result into
			// an error return; see TestClusterValidationSyncer_SyncOnce's sibling case below for the
			// LogOnly branch of the same decision.
			name:    "validation unknown with ReportError -- condition set to Unknown, requeue scheduled, error returned",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithUnknownReportError(
				"InternalError", "failed to reach Azure", "Unable to verify.",
			),
			wantErr:       true,
			wantCondition: &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "InternalError", Message: "Unable to verify."},
			wantEnqueue:   true,
		},
		{
			// Covers handleRequeue's nil guard: EarliestRetryAfter == nil must skip cooldown/requeue
			// without otherwise affecting the condition write.
			name:    "validation fails with nil EarliestRetryAfter -- condition still set, no cooldown or requeue scheduled",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			).WithEarliestRetryAfter(nil),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   false,
		},
		{
			name:    "validation unknown with LogOnly -- condition set to Unknown, requeue still scheduled, no error returned",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
				"TransientIssue", "temporary network blip", "Temporarily unable to verify.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "TransientIssue", Message: "Temporarily unable to verify."},
			wantEnqueue:   true,
		},
		{
			name:    "validation skipped with no prior condition -- no condition persisted, no requeue",
			setupDB: defaultSetupDB,
			validation: NewMockClusterValidation(testValidationName).WithSkipped(
				"NotApplicable", "cluster does not need this check", "Not applicable.",
			),
			wantConditionAbsent: true,
			wantEnqueue:         false,
		},
		{
			name: "validation skipped with prior condition -- condition removed, no requeue",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
				spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				spc.Status.Validations = []metav1.Condition{
					{
						Type:    testValidationName,
						Status:  metav1.ConditionFalse,
						Reason:  "PreviouslyFailed",
						Message: "previously failed",
					},
				}
				_, err = spcCRUD.Replace(ctx, spc, nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName).WithSkipped(
				"NotApplicable", "cluster does not need this check", "Not applicable.",
			),
			wantConditionAbsent: true,
			wantEnqueue:         false,
		},
		{
			name: "already-succeeded validation -- re-runs and overwrites with Failed",
			setupDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				defaultSetupDB(t, ctx, mockDB)
				spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
				spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				spc.Status.Validations = []metav1.Condition{
					{
						Type:   testValidationName,
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					},
				}
				_, err = spcCRUD.Replace(ctx, spc, nil)
				require.NoError(t, err)
			},
			validation: NewMockClusterValidation(testValidationName).WithFailed(
				"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
			),
			wantCondition: &metav1.Condition{Status: metav1.ConditionFalse, Reason: "QuotaExceeded", Message: "Quota exceeded for this subscription."},
			wantEnqueue:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tc.setupDB != nil {
				tc.setupDB(t, ctx, mockDB)
			}

			fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
			syncer, enqueuer := newTestSyncer(mockDB, tc.validation, fakeClock)

			err := syncer.SyncOnce(ctx, newTestClusterKey())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.wantEnqueue {
				require.NotEmpty(t, enqueuer.enqueuedKeys, "expected a requeue to be scheduled")
			} else {
				require.Empty(t, enqueuer.enqueuedKeys, "expected no requeue to be scheduled")
			}

			if tc.wantConditionAbsent {
				spc, spcErr := mockDB.ServiceProviderClusters(
					testSubscriptionID, testResourceGroup, testClusterName,
				).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, spcErr)

				cond := meta.FindStatusCondition(spc.Status.Validations, testValidationName)
				assert.Nil(t, cond, "expected validation condition to be absent")
			}

			if tc.wantCondition != nil {
				spc, spcErr := mockDB.ServiceProviderClusters(
					testSubscriptionID, testResourceGroup, testClusterName,
				).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, spcErr)

				cond := meta.FindStatusCondition(spc.Status.Validations, testValidationName)
				require.NotNil(t, cond, "expected validation condition to be set")
				assert.Equal(t, tc.wantCondition.Status, cond.Status)
				assert.Equal(t, tc.wantCondition.Reason, cond.Reason)
				assert.Equal(t, tc.wantCondition.Message, cond.Message)
			}
		})
	}
}

// TestClusterValidationSyncer_ProbeTimes verifies that the syncer records the
// start time of each Failed run, rewritten while the validation keeps failing
// at most once per ValidationProbeRewriteInterval, left untouched by Unknown
// runs, and removed when the validation passes or is skipped. The operation
// controllers use these probe times to distinguish a freshly re-confirmed
// failure from one that has not been re-checked lately.
func TestClusterValidationSyncer_ProbeTimes(t *testing.T) {
	t.Parallel()

	getSPC := func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *coreapi.ServiceProviderCluster {
		t.Helper()
		spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName).
			Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		return spc
	}

	t.Run("failed run records a probe time at the clock's now", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, _ := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithFailed(
			"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
		), fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc := getSPC(t, ctx, mockDB)
		probeTime, ok := spc.Status.ValidationProbeTimes[testValidationName]
		require.True(t, ok, "expected a probe time to be recorded")
		assert.True(t, fixedNow.Equal(probeTime.Time), "probe time must be the clock's now, got %s", probeTime.Time)
	})

	t.Run("probe time is the run's start, not its completion", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		validation := NewMockClusterValidation(testValidationName).WithValidateFunc(func(context.Context) validationutils.ValidationResult {
			fakeClock.SetTime(fixedNow.Add(90 * time.Second))
			return validationutils.FailedValidation("QuotaExceeded", "Quota exceeded for this subscription.", "quota exceeded")
		})
		syncer, _ := newTestSyncer(mockDB, validation, fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc := getSPC(t, ctx, mockDB)
		probeTime, ok := spc.Status.ValidationProbeTimes[testValidationName]
		require.True(t, ok, "expected a probe time to be recorded")
		assert.True(t, fixedNow.Equal(probeTime.Time), "probe time must be the run's start time, got %s", probeTime.Time)
	})

	t.Run("unchanged outcome still refreshes the probe time", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		// Seed a False condition with an old probe time, as a previous run
		// would have left behind.
		spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
		spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		spc.Status.Validations = []metav1.Condition{{
			Type: testValidationName, Status: metav1.ConditionFalse, Reason: "QuotaExceeded",
			LastTransitionTime: metav1.NewTime(fixedNow.Add(-10 * time.Minute)),
		}}
		spc.Status.ValidationProbeTimes = map[string]metav1.Time{
			testValidationName: metav1.NewTime(fixedNow.Add(-10 * time.Minute)),
		}
		_, err = spcCRUD.Replace(ctx, spc, nil)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, _ := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithFailed(
			"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
		), fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc = getSPC(t, ctx, mockDB)
		sprobeTime, ok := spc.Status.ValidationProbeTimes[testValidationName]
		require.True(t, ok, "expected a probe time to be recorded")
		assert.True(t, fixedNow.Equal(sprobeTime.Time), "probe time must advance even when the outcome is unchanged, got %s", sprobeTime.Time)
		// The condition itself is unchanged, so its LastTransitionTime is not.
		cond := meta.FindStatusCondition(spc.Status.Validations, testValidationName)
		require.NotNil(t, cond)
		assert.True(t, fixedNow.Add(-10*time.Minute).Equal(cond.LastTransitionTime.Time), "condition LastTransitionTime must be unchanged, got %s", cond.LastTransitionTime.Time)
	})

	t.Run("unknown outcome does not refresh the probe time", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		oldProbe := metav1.NewTime(fixedNow.Add(-8 * time.Minute))
		spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
		spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		spc.Status.Validations = []metav1.Condition{{
			Type: testValidationName, Status: metav1.ConditionFalse, Reason: "QuotaExceeded",
			LastTransitionTime: metav1.NewTime(fixedNow.Add(-10 * time.Minute)),
		}}
		spc.Status.ValidationProbeTimes = map[string]metav1.Time{testValidationName: oldProbe}
		_, err = spcCRUD.Replace(ctx, spc, nil)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, _ := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
			"TransientIssue", "temporary network blip", "Temporarily unable to verify.",
		), fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc = getSPC(t, ctx, mockDB)
		probeTime, ok := spc.Status.ValidationProbeTimes[testValidationName]
		require.True(t, ok, "expected the probe time to be retained")
		assert.True(t, oldProbe.Equal(&probeTime), "probe time must not advance on an Unknown outcome, got %s", probeTime.Time)
	})

	t.Run("skipped run removes the probe time", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
		spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		spc.Status.ValidationProbeTimes = map[string]metav1.Time{
			testValidationName: metav1.NewTime(fixedNow.Add(-time.Minute)),
		}
		_, err = spcCRUD.Replace(ctx, spc, nil)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, _ := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithSkipped(
			"NotApplicable", "cluster does not need this check", "Not applicable.",
		), fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc = getSPC(t, ctx, mockDB)
		_, ok := spc.Status.ValidationProbeTimes[testValidationName]
		assert.False(t, ok, "expected the probe time to be removed for a skipped validation")
	})

	t.Run("failed re-check within the rewrite interval keeps the stored probe", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		recentProbe := metav1.NewTime(fixedNow.Add(-validationutils.ValidationProbeRewriteInterval + time.Second))
		spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
		spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		spc.Status.Validations = []metav1.Condition{{
			Type: testValidationName, Status: metav1.ConditionFalse, Reason: "QuotaExceeded",
			Message:            "Quota exceeded for this subscription.",
			LastTransitionTime: metav1.NewTime(fixedNow.Add(-10 * time.Minute)),
		}}
		spc.Status.ValidationProbeTimes = map[string]metav1.Time{testValidationName: recentProbe}
		_, err = spcCRUD.Replace(ctx, spc, nil)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, _ := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithFailed(
			"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
		), fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc = getSPC(t, ctx, mockDB)
		probeTime, ok := spc.Status.ValidationProbeTimes[testValidationName]
		require.True(t, ok, "expected the probe time to be retained")
		assert.True(t, recentProbe.Equal(&probeTime), "a probe younger than the rewrite interval must not be rewritten, got %s", probeTime.Time)
	})

	t.Run("passed run removes the probe time", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
		spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		spc.Status.ValidationProbeTimes = map[string]metav1.Time{
			testValidationName: metav1.NewTime(fixedNow.Add(-time.Minute)),
		}
		_, err = spcCRUD.Replace(ctx, spc, nil)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, _ := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithPassed(), fakeClock)
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		spc = getSPC(t, ctx, mockDB)
		_, ok := spc.Status.ValidationProbeTimes[testValidationName]
		assert.False(t, ok, "expected the probe time to be removed for a passed validation")
	})

	t.Run("write conflict still schedules the retry cooldown", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, enqueuer := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithFailed(
			"QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.",
		), fakeClock)
		syncer.serviceProviderClusterLister = &concurrentWriterServiceProviderClusterLister{
			ServiceProviderClusterLister: syncer.serviceProviderClusterLister,
			resourcesDBClient:            mockDB,
			conflictOnGet:                2,
		}
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()), "a write conflict must not surface as a sync error")

		assert.False(t, syncer.retryCooldownChecker.CanSync(ctx, newTestClusterKey()), "the retry cooldown must be set even when the document write conflicts")
		require.NotEmpty(t, enqueuer.enqueuedKeys, "a delayed requeue must be scheduled after a write conflict")
		assert.Positive(t, enqueuer.enqueuedDurations[0], "the delayed requeue must be scheduled in the future")

		spc := getSPC(t, ctx, mockDB)
		_, ok := spc.Status.ValidationProbeTimes[testValidationName]
		assert.False(t, ok, "the conflicted write must not have persisted a probe time")
	})

	t.Run("passed write conflict retries on the non-passed cadence", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
		_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, newTestCluster(t), nil)
		require.NoError(t, err)
		_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
		require.NoError(t, err)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, newTestCluster(t).ID)
		require.NoError(t, err)

		spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
		spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		spc.Status.Validations = []metav1.Condition{{
			Type: testValidationName, Status: metav1.ConditionFalse, Reason: "QuotaExceeded",
			LastTransitionTime: metav1.NewTime(fixedNow.Add(-10 * time.Minute)),
		}}
		_, err = spcCRUD.Replace(ctx, spc, nil)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
		syncer, enqueuer := newTestSyncer(mockDB, NewMockClusterValidation(testValidationName).WithPassed(), fakeClock)
		syncer.serviceProviderClusterLister = &concurrentWriterServiceProviderClusterLister{
			ServiceProviderClusterLister: syncer.serviceProviderClusterLister,
			resourcesDBClient:            mockDB,
			conflictOnGet:                2,
		}
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()), "a write conflict must not surface as a sync error")

		maxDelay := 90*time.Second + time.Second
		assert.LessOrEqual(t, syncer.retryCooldownChecker.TimeUntilReady(newTestClusterKey()), maxDelay,
			"a lost Passed result must not apply the long passed-retry cooldown")
		require.NotEmpty(t, enqueuer.enqueuedKeys, "a delayed requeue must be scheduled so the lost Passed result is recomputed")
		assert.LessOrEqual(t, enqueuer.enqueuedDurations[0], maxDelay, "the requeue must use the non-passed retry cadence")

		cond := meta.FindStatusCondition(getSPC(t, ctx, mockDB).Status.Validations, testValidationName)
		require.NotNil(t, cond, "expected the stored condition to remain")
		assert.Equal(t, metav1.ConditionFalse, cond.Status, "the conflicted write must not have persisted the Passed result")
	})
}

// concurrentWriterServiceProviderClusterLister simulates a concurrent writer: on the conflictOnGet-th
// Get it returns the stored document and then replaces it in the database, so a write based on the
// returned copy hits an optimistic-concurrency conflict.
type concurrentWriterServiceProviderClusterLister struct {
	corelisters.ServiceProviderClusterLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	conflictOnGet     int
	gets              int
}

func (l *concurrentWriterServiceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.ServiceProviderCluster, error) {
	spc, err := l.ServiceProviderClusterLister.Get(ctx, subscriptionID, resourceGroupName, clusterName)
	l.gets++
	if err != nil || l.gets != l.conflictOnGet {
		return spc, err
	}
	spcCRUD := l.resourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)
	if _, err := spcCRUD.Replace(ctx, spc.DeepCopy(), nil); err != nil {
		return nil, err
	}
	return spc, nil
}

// TestClusterValidationSyncer_ShouldWriteCondition unit-tests the suppression decision in isolation from
// Cosmos/DB plumbing, covering the boundary cases around maxConsecutiveUnknownsBeforeWrite.
func TestClusterValidationSyncer_ShouldWriteCondition(t *testing.T) {
	syncer := &clusterValidationSyncer{}

	t.Run("no previously stored condition -- always write, even mid-streak", func(t *testing.T) {
		assert.True(t, syncer.shouldWriteCondition(nil, 5))
	})

	// shouldWriteCondition only checks previousCondition's nilness, not its Status. Vary Status here to
	// lock that contract: suppression depends solely on consecutiveUnknowns.
	previousConditionFixtures := []struct {
		name      string
		condition *metav1.Condition
	}{
		{
			name:      "Unknown",
			condition: &metav1.Condition{Type: testValidationName, Status: metav1.ConditionUnknown},
		},
		{
			name:      "Failed",
			condition: &metav1.Condition{Type: testValidationName, Status: metav1.ConditionFalse, Reason: "PreviouslyFailed"},
		},
		{
			name:      "Passed",
			condition: &metav1.Condition{Type: testValidationName, Status: metav1.ConditionTrue, Reason: "AsExpected"},
		},
	}

	scenarios := []struct {
		name                string
		consecutiveUnknowns int
		want                bool
	}{
		{
			name:                "non-Unknown result (streak reset to 0) -- write",
			consecutiveUnknowns: 0,
			want:                true,
		},
		{
			name:                "first Unknown in streak -- suppress",
			consecutiveUnknowns: 1,
			want:                false,
		},
		{
			name:                "streak exactly at threshold -- suppress (boundary)",
			consecutiveUnknowns: maxConsecutiveUnknownsBeforeWrite,
			want:                false,
		},
		{
			name:                "streak one past threshold -- write (boundary)",
			consecutiveUnknowns: maxConsecutiveUnknownsBeforeWrite + 1,
			want:                true,
		},
	}

	for _, fixture := range previousConditionFixtures {
		for _, scenario := range scenarios {
			t.Run(fixture.name+" prior, "+scenario.name, func(t *testing.T) {
				assert.Equal(t, scenario.want, syncer.shouldWriteCondition(fixture.condition, scenario.consecutiveUnknowns))
			})
		}
	}
}

// TestClusterValidationSyncer_TrackConsecutiveUnknowns unit-tests the per-key streak bookkeeping in
// isolation: incrementing across consecutive Unknown results, resetting on a non-Unknown result, and
// tracking each HCPClusterKey independently. Each test case is a sequence of steps run against a single
// fresh syncer, asserting the returned count after every step.
func TestClusterValidationSyncer_TrackConsecutiveUnknowns(t *testing.T) {
	keyA := newTestClusterKey()
	keyB := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroup,
		HCPClusterName:    "other-cluster",
	}

	type step struct {
		key    controllerutils.HCPClusterKey
		status metav1.ConditionStatus
		want   int
	}

	testCases := []struct {
		name  string
		steps []step
	}{
		{
			name: "increments on consecutive Unknown results",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyA, status: metav1.ConditionUnknown, want: 3},
			},
		},
		{
			name: "non-Unknown result resets the streak to 0",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyA, status: metav1.ConditionFalse, want: 0},
			},
		},
		{
			name: "streak restarts at 1 after a reset, not continuing the pre-reset count",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyA, status: metav1.ConditionTrue, want: 0},
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
			},
		},
		{
			name: "non-Unknown result with no prior streak stays at 0",
			steps: []step{
				{key: keyA, status: metav1.ConditionFalse, want: 0},
			},
		},
		{
			name: "keys are tracked independently",
			steps: []step{
				{key: keyA, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 2},
				{key: keyB, status: metav1.ConditionUnknown, want: 1},
				{key: keyA, status: metav1.ConditionUnknown, want: 3},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			syncer := &clusterValidationSyncer{consecutiveUnknownCounts: lru.New(consecutiveUnknownCountsCacheCapacity)}
			for i, s := range tc.steps {
				condition := metav1.Condition{Type: testValidationName, Status: s.status}
				got := syncer.trackConsecutiveUnknowns(s.key, condition)
				assert.Equalf(t, s.want, got, "step %d: key=%s, status=%s", i, s.key.HCPClusterName, s.status)
			}
		})
	}
}

// TestClusterValidationSyncer_ConsecutiveUnknownSuppression exercises the consecutive-Unknown suppression
// policy end-to-end across repeated SyncOnce calls: a previously stored Failed condition should survive the
// first maxConsecutiveUnknownsBeforeWrite consecutive Unknown results untouched (and skip the Cosmos write
// each time, per the equality.Semantic.DeepEqual guard), then get overwritten with Unknown once the streak
// persists past the threshold.
func TestClusterValidationSyncer_ConsecutiveUnknownSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newTestCluster(t)
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
	require.NoError(t, err)

	spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
	spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	spc.Status.Validations = []metav1.Condition{
		{
			Type:    testValidationName,
			Status:  metav1.ConditionFalse,
			Reason:  "PreviouslyFailed",
			Message: "previously failed",
		},
	}
	_, err = spcCRUD.Replace(ctx, spc, nil)
	require.NoError(t, err)

	validation := NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
		"InternalError", "failed to reach Azure", "Unable to verify.",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, _ := newTestSyncer(mockDB, validation, fakeClock)

	for i := 1; i <= maxConsecutiveUnknownsBeforeWrite; i++ {
		fakeClock.SetTime(fakeClock.Now().Add(time.Hour))

		before, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

		after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)

		cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
		require.NotNil(t, cond)
		assert.Equalf(t, metav1.ConditionFalse, cond.Status, "attempt %d: previous condition should be preserved", i)
		assert.Equalf(t, "PreviouslyFailed", cond.Reason, "attempt %d: previous condition should be preserved", i)
		assert.Equalf(t, before.CosmosETag, after.CosmosETag, "attempt %d: Cosmos write should have been skipped", i)
	}

	// The next attempt exceeds the threshold, so the Unknown condition finally overwrites the stored one.
	fakeClock.SetTime(fakeClock.Now().Add(time.Hour))

	before, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

	after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionUnknown, cond.Status)
	assert.Equal(t, "InternalError", cond.Reason)
	assert.NotEqual(t, before.CosmosETag, after.CosmosETag, "expected a Cosmos write once the suppression threshold was exceeded")
}

// TestClusterValidationSyncer_CooldownSuppression verifies that when the
// retryCooldownChecker's cooldown is active for a key, SyncOnce returns
// immediately without performing validation, and schedules a re-enqueue.
func TestClusterValidationSyncer_CooldownSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newTestCluster(t)
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
	require.NoError(t, err)

	validation := NewMockClusterValidation(testValidationName).WithFailed(
		"ShouldNotRun", "should not run", "should not run",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, enqueuer := newTestSyncer(mockDB, validation, fakeClock)

	key := newTestClusterKey()
	syncer.retryCooldownChecker.SetCooldown(key, 60*time.Second)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err, "SyncOnce should return nil when cooldown is active")

	require.NotEmpty(t, enqueuer.enqueuedKeys, "should have re-enqueued after cooldown skip")
	assert.Greater(t, enqueuer.enqueuedDurations[0], time.Duration(0), "enqueue duration should be positive")
}

func TestClusterValidationSyncer_ProbeRewriteWriteInterval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		durations  []time.Duration
		retryDelay time.Duration
	}{
		{
			name:       "short runs with short retries",
			durations:  []time.Duration{59 * time.Second, time.Second},
			retryDelay: 61 * time.Second,
		},
		{
			name:       "slow runs must not rewrite early",
			durations:  []time.Duration{7 * time.Minute, time.Second},
			retryDelay: 90 * time.Second,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			cluster := newTestCluster(t)
			_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
			require.NoError(t, err)
			_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
			require.NoError(t, err)
			_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
			require.NoError(t, err)

			fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
			var run int
			validation := NewMockClusterValidation(testValidationName).WithValidateFunc(func(context.Context) validationutils.ValidationResult {
				d := tc.durations[run%len(tc.durations)]
				run++
				fakeClock.SetTime(fakeClock.Now().Add(d))
				result := validationutils.FailedValidation("QuotaExceeded", "Quota exceeded for this subscription.", "quota exceeded")
				result.EarliestRetryAfter = ptr.To(tc.retryDelay)
				return result
			})

			syncer, _ := newTestSyncer(mockDB, validation, fakeClock)

			spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
			var lastWrite time.Time
			rewrites := 0
			for i := 0; i < 20; i++ {
				// Each run starts one retry delay after the previous run completed.
				fakeClock.SetTime(fakeClock.Now().Add(tc.retryDelay + time.Second))

				before, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))
				after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)

				probe := after.Status.ValidationProbeTimes[testValidationName]
				require.Falsef(t, probe.IsZero(), "run %d: a failing validation must always record a probe", i)

				completion := fakeClock.Now()
				if tc.name == "slow runs must not rewrite early" {
					if tc.durations[(run-1)%len(tc.durations)] > validationutils.ValidationProbeRewriteInterval {
						assert.Truef(t, probe.Time.Equal(completion.Add(-tc.durations[(run-1)%len(tc.durations)])),
							"run %d: a slow failure must store its start time", i)
					}
				} else {
					if beforeProbe, ok := before.Status.ValidationProbeTimes[testValidationName]; ok {
						assert.LessOrEqualf(t, completion.Sub(beforeProbe.Time), validationutils.ValidationProbeFreshnessWindow,
							"run %d: the stored probe went stale while the validation was re-checking on schedule", i)
					}
					assert.LessOrEqualf(t, completion.Sub(probe.Time), validationutils.ValidationProbeFreshnessWindow,
						"run %d: the freshly written probe exceeds the freshness window", i)
				}

				if !probe.Time.Equal(before.Status.ValidationProbeTimes[testValidationName].Time) {
					if rewrites > 0 {
						assert.GreaterOrEqualf(t, fakeClock.Since(lastWrite), validationutils.ValidationProbeRewriteInterval,
							"rewrite %d landed too soon after the previous write", rewrites)
					}
					lastWrite = fakeClock.Now()
					rewrites++
				}
			}
			assert.GreaterOrEqual(t, rewrites, 3, "the schedule must produce several rewrites for the interval assertions to be meaningful")
		})
	}
}

// TestClusterValidationSyncer_ProbeRewriteRestartFallback covers the fallback used when no
// write is on record (syncer restart or leader change): a stored probe at least
// ValidationProbeRewriteInterval old is rewritten, while a younger one is left alone.
func TestClusterValidationSyncer_ProbeRewriteRestartFallback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		probeAge    time.Duration
		wantRewrite bool
	}{
		{name: "stored probe older than the interval is rewritten", probeAge: validationutils.ValidationProbeRewriteInterval + time.Minute, wantRewrite: true},
		{name: "stored probe younger than the interval is left alone", probeAge: time.Minute, wantRewrite: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			cluster := newTestCluster(t)
			_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
			require.NoError(t, err)
			_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
			require.NoError(t, err)
			_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
			require.NoError(t, err)

			spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
			spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			// Truncate to whole seconds: the mock DB round-trips timestamps at
			// second precision, so sub-second differences would be indistinguishable
			// from a rewrite.
			storedProbe := time.Now().Add(-tc.probeAge).Truncate(time.Second)
			spc.Status.ValidationProbeTimes = map[string]metav1.Time{
				testValidationName: metav1.NewTime(storedProbe),
			}
			_, err = spcCRUD.Replace(ctx, spc, nil)
			require.NoError(t, err)

			fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
			validation := NewMockClusterValidation(testValidationName).WithFailed("QuotaExceeded", "quota exceeded", "Quota exceeded for this subscription.")
			// A fresh syncer has no in-memory write record, exercising the fallback.
			syncer, _ := newTestSyncer(mockDB, validation, fakeClock)
			fakeClock.SetTime(storedProbe.Add(tc.probeAge)) // now is exactly probeAge after the stored probe

			require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

			after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			probe := after.Status.ValidationProbeTimes[testValidationName]
			require.False(t, probe.IsZero())
			assert.Equalf(t, tc.wantRewrite, !probe.Time.Equal(storedProbe),
				"expected rewrite=%v", tc.wantRewrite)
		})
	}
// TestClusterValidationSyncer_PropagationDeferralSurvivesLookupError covers the regression
// where a single transient role-assignment lookup error (Unknown,
// RoleAssignmentPropagationCheckFailed) ended an established RoleAssignmentPropagationPending
// deferral: once the consecutive-Unknown streak exceeded the suppression threshold the
// CheckFailed reason was written immediately, and because the status did not change,
// LastTransitionTime was not refreshed — leaving an old condition that the operations
// controller no longer treats as propagation-pending, terminally failing the operation on
// the next reconciliation. The stored deferral must keep the Pending reason while its
// evidence can still be valid.
func TestClusterValidationSyncer_PropagationDeferralSurvivesLookupError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newTestCluster(t)
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
	require.NoError(t, err)

	spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
	spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	spc.Status.Validations = []metav1.Condition{
		{
			Type:               testValidationName,
			Status:             metav1.ConditionUnknown,
			Reason:             "RoleAssignmentPropagationPending",
			Message:            "Role assignments granting these permissions were created recently and have not taken effect yet.",
			LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
	}
	_, err = spcCRUD.Replace(ctx, spc, nil)
	require.NoError(t, err)

	validation := NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
		"RoleAssignmentPropagationCheckFailed",
		"missing permissions; role assignment lookup failed: ARM 500",
		"Unable to verify whether recently created role assignments explain the missing permissions.",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, _ := newTestSyncer(mockDB, validation, fakeClock)

	// Exceed the consecutive-Unknown suppression threshold so the CheckFailed result is
	// written immediately, which is where the deferral used to be lost. The clock advances
	// between attempts because every Unknown result schedules a retry cooldown.
	for i := 0; i <= maxConsecutiveUnknownsBeforeWrite; i++ {
		fakeClock.SetTime(fakeClock.Now().Add(time.Hour))
		require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))
	}

	after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionUnknown, cond.Status)
	assert.Equal(t, "RoleAssignmentPropagationPending", cond.Reason,
		"an established propagation deferral must survive a transient lookup failure")
	assert.Contains(t, cond.Message, "Unable to verify",
		"the lookup failure should still be visible in the condition message")
}

// TestClusterValidationSyncer_PropagationPendingBypassesSuppression covers the case where
// propagation is detected while an older stored failure exists: the consecutive-Unknown
// suppression would keep the False condition in place for the first ten Unknown results,
// and the operation controllers can terminally fail on that stale failure even though the
// propagation deferral that would hold the operation open was detected. A
// propagation-pending condition must therefore be written immediately.
func TestClusterValidationSyncer_PropagationPendingBypassesSuppression(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newTestCluster(t)
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroup).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, newTestSubscription(), nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockDB, cluster.ID)
	require.NoError(t, err)

	// A permission failure that started nine minutes ago, as a previous run left behind.
	spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroup, testClusterName)
	spc, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	spc.Status.Validations = []metav1.Condition{{
		Type:               testValidationName,
		Status:             metav1.ConditionFalse,
		Reason:             "MissingRequiredPermissions",
		Message:            "missing permissions",
		LastTransitionTime: metav1.NewTime(time.Now().Add(-9 * time.Minute)),
	}}
	_, err = spcCRUD.Replace(ctx, spc, nil)
	require.NoError(t, err)

	// The customer has since created the corrective assignments; the next run detects
	// propagation. This is the first Unknown result, so the suppression would normally
	// keep the stored False condition in place.
	validation := NewMockClusterValidation(testValidationName).WithUnknownLogOnly(
		"RoleAssignmentPropagationPending",
		"missing permissions; covered by role assignments created within the last 60m0s that may still be propagating",
		"Role assignments granting these permissions were created recently and have not taken effect yet. No configuration change is required.",
	)

	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer, _ := newTestSyncer(mockDB, validation, fakeClock)
	require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))

	after, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	cond := meta.FindStatusCondition(after.Status.Validations, testValidationName)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionUnknown, cond.Status,
		"the propagation-pending result must be written despite the consecutive-Unknown suppression")
	assert.Equal(t, "RoleAssignmentPropagationPending", cond.Reason,
		"the stored failure must be replaced by the propagation deferral, not hidden behind it")
}
