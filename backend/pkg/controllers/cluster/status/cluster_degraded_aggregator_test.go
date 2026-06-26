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

package status

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	sharedstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/status"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// newTestClusterForAggregator builds a minimal HCPOpenShiftCluster suitable
// for the aggregator tests. Callers can layer in pre-existing
// Status.Conditions via the opts hook to exercise the "skip write when
// unchanged" path.
func newTestClusterForAggregator(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func TestClusterDegradedAggregator_SyncOnce(t *testing.T) {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	// thirtySecondInertia / fiveMinuteOverrideInertia mirror the two
	// inertia setups exercised in union_condition_test.go so the table
	// stays readable.
	thirtySecondInertia := sharedstatus.MustNewInertia(30 * time.Second).Inertia
	fiveMinuteOverrideInertia := sharedstatus.MustNewInertia(
		30*time.Second,
		sharedstatus.InertiaController{ControllerNameMatcher: regexp.MustCompile(`^SlowController$`), Duration: 5 * time.Minute},
	).Inertia

	tests := []struct {
		name string

		controllers []*api.Controller
		inertia     sharedstatus.Inertia
		// initialConditions, if set, is layered onto the cluster before SyncOnce
		// runs. Used to drive the "no-op when conditions unchanged" case.
		initialConditions []metav1.Condition

		expectStatus  metav1.ConditionStatus
		expectReason  string
		expectMessage string
	}{
		{
			name:          "no controllers under the cluster -> Unknown/NoData",
			controllers:   nil,
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  "NoData",
			expectMessage: "",
		},
		{
			name: "all controllers report Degraded=False -> aggregate False/AsExpected",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
				controllerUnder(parentResourceID, "BController", metav1.ConditionFalse, "NoErrors", "ok", 1*time.Minute),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: fine\nBController: ok",
		},
		{
			name: "bad controller within 30s inertia is hidden -> aggregate stays default",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 5*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: boom",
		},
		{
			name: "bad controller past 30s inertia flips aggregate to True",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 31*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "per-controller inertia override delays SlowController",
			controllers: []*api.Controller{
				// SlowController has 5m inertia; 2m old is still fresh -> NOT elder.
				controllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "settling", 2*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "SlowController: settling",
		},
		{
			name: "per-controller inertia override + elder default controller -> aggregate flips",
			controllers: []*api.Controller{
				// SlowController has 5m inertia; 2m fresh -> not elder.
				controllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "settling", 2*time.Minute),
				// NormalController uses default 30s inertia; 1m old -> elder.
				controllerUnder(parentResourceID, "NormalController", metav1.ConditionTrue, "Failed", "boom", 1*time.Minute),
			},
			inertia:      fiveMinuteOverrideInertia,
			expectStatus: metav1.ConditionTrue,
			// Once elderBad is non-empty the aggregated reason/message enumerate ALL bad
			// sources (matching library-go).
			expectReason:  "NormalController_Failed::SlowController_Failed",
			expectMessage: "NormalController: boom\nSlowController: settling",
		},
		{
			name: "nil inertia propagates a fresh bad source immediately",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Second),
			},
			inertia:       nil,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "missing Degraded condition is hidden within inertia (first reconcile)",
			controllers: []*api.Controller{
				// Controller doc exists but has not reported a Degraded condition yet.
				// On first reconcile the cache records "now" as first-observed-bad, so
				// the synthesized entry is age=0 -> within the 30s window -> hidden.
				{
					CosmosMetadata: api.CosmosMetadata{
						ResourceID:   api.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + api.ControllerResourceTypeName + "/QuietController")),
						PartitionKey: strings.ToLower(parentResourceID.SubscriptionID),
					},
					ExternalID: parentResourceID,
				},
			},
			inertia:      thirtySecondInertia,
			expectStatus: metav1.ConditionFalse,
			expectReason: "AsExpected",
			// Even on the all-good path UnionCondition surfaces the bad source's
			// message so the aggregate stays attributable, just with a default
			// (False/AsExpected) status.
			expectMessage: "QuietController: Controller has not reported a Degraded condition",
		},
		{
			name: "missing Degraded condition flips immediately with nil inertia",
			controllers: []*api.Controller{
				{
					CosmosMetadata: api.CosmosMetadata{
						ResourceID:   api.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + api.ControllerResourceTypeName + "/QuietController")),
						PartitionKey: strings.ToLower(parentResourceID.SubscriptionID),
					},
					ExternalID: parentResourceID,
				},
			},
			inertia:       nil,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "QuietController_MissingDegradedCondition",
			expectMessage: "QuietController: Controller has not reported a Degraded condition",
		},
		{
			name: "Degraded=Unknown past inertia flips (uses condition's real LastTransitionTime)",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionUnknown, "Investigating", "still figuring out", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			// Unknown != defaultStatus, so it's counted as bad. With a real 1m old
			// LastTransitionTime it's past the 30s window -> flips. Aggregate
			// status remains Unknown because Unknown is the only bad status here.
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  "AController_Investigating",
			expectMessage: "AController: still figuring out",
		},
		{
			name: "no-op when computed aggregate equals existing condition",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			// Pre-seed the cluster with the same Degraded=False/AsExpected condition
			// that the aggregator will compute. The aggregator must detect the no-op
			// and skip the Replace; we still verify the resulting condition matches.
			initialConditions: []metav1.Condition{
				{
					Type:    sharedstatus.DegradedConditionType,
					Status:  metav1.ConditionFalse,
					Reason:  "AsExpected",
					Message: "AController: fine",
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: fine",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			existing := newTestClusterForAggregator(func(c *api.HCPOpenShiftCluster) {
				if len(tc.initialConditions) > 0 {
					c.Status.Conditions = append([]metav1.Condition{}, tc.initialConditions...)
				}
			})

			seed := []any{existing}
			for _, ctrl := range tc.controllers {
				seed = append(seed, ctrl)
			}
			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			clock := clocktesting.NewFakePassiveClock(fixedNow)
			syncer := &clusterDegradedAggregator{
				cooldownChecker:   alwaysSyncCooldownChecker{},
				clusterLister:     &listertesting.DBClusterLister{ResourcesDBClient: mockDB},
				controllerLister:  &listertesting.DBControllerLister{ResourcesDBClient: mockDB},
				resourcesDBClient: mockDB,
				inertia:           tc.inertia,
				clock:             clock,
				firstObservedBad:  sharedstatus.NewFirstObservedBadCache(clock),
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, sharedstatus.DegradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on the cluster")
			assert.Equal(t, tc.expectStatus, cond.Status, "status")
			assert.Equal(t, tc.expectReason, cond.Reason, "reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "message")
		})
	}
}

// TestClusterDegradedAggregator_MissingDegradedFlipsAfterInertia drives two
// SyncOnce passes against the same syncer with the fake clock advanced past
// the inertia window between them. On the first pass the missing Degraded
// condition is recorded in the first-observed-bad cache but hidden by
// inertia (aggregate stays Degraded=False/AsExpected). On the second pass
// the clock has moved past the 30s default window, so the synthesized
// missing entry is now elder and the aggregate must flip to Degraded=True
// with MissingDegradedCondition surfaced in the reason and message.
//
// Kept separate from the table-driven TestClusterDegradedAggregator_SyncOnce
// because that table runs each case with a fresh syncer and clock; this
// scenario requires state to persist across reconciles.
func TestClusterDegradedAggregator_MissingDegradedFlipsAfterInertia(t *testing.T) {
	ctx := context.Background()

	parentResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	quietController := &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   api.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + api.ControllerResourceTypeName + "/QuietController")),
			PartitionKey: strings.ToLower(parentResourceID.SubscriptionID),
		},
		ExternalID: parentResourceID,
	}

	existing := newTestClusterForAggregator()
	mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{existing, quietController})
	require.NoError(t, err)

	clock := clocktesting.NewFakePassiveClock(fixedNow)
	syncer := &clusterDegradedAggregator{
		cooldownChecker:   alwaysSyncCooldownChecker{},
		clusterLister:     &listertesting.DBClusterLister{ResourcesDBClient: mockDB},
		controllerLister:  &listertesting.DBControllerLister{ResourcesDBClient: mockDB},
		resourcesDBClient: mockDB,
		inertia:           sharedstatus.MustNewInertia(sharedstatus.DefaultInertia).Inertia,
		clock:             clock,
		firstObservedBad:  sharedstatus.NewFirstObservedBadCache(clock),
	}
	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	// First reconcile at fixedNow: cache records first-observed-bad as now,
	// synthesized entry is age=0 -> within the 30s inertia -> aggregate stays
	// at the default Degraded=False/AsExpected.
	require.NoError(t, syncer.SyncOnce(ctx, key))

	afterFirst, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
	require.NoError(t, err)
	firstCond := apimeta.FindStatusCondition(afterFirst.Status.Conditions, sharedstatus.DegradedConditionType)
	require.NotNil(t, firstCond)
	assert.Equal(t, metav1.ConditionFalse, firstCond.Status, "first reconcile: still inside inertia window")
	assert.Equal(t, "AsExpected", firstCond.Reason)
	assert.Equal(t, "QuietController: Controller has not reported a Degraded condition", firstCond.Message,
		"the missing source is still reported in the message, just not in the status")

	// Advance the clock past the 30s default inertia. The cache entry is
	// sticky, so the synthesized entry is now elder.
	clock.SetTime(fixedNow.Add(sharedstatus.DefaultInertia + time.Second))
	require.NoError(t, syncer.SyncOnce(ctx, key))

	afterSecond, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
	require.NoError(t, err)
	secondCond := apimeta.FindStatusCondition(afterSecond.Status.Conditions, sharedstatus.DegradedConditionType)
	require.NotNil(t, secondCond)
	assert.Equal(t, metav1.ConditionTrue, secondCond.Status, "second reconcile: past inertia -> aggregate must flip")
	assert.Equal(t, "QuietController_MissingDegradedCondition", secondCond.Reason)
	assert.Equal(t, "QuietController: Controller has not reported a Degraded condition", secondCond.Message)
}
