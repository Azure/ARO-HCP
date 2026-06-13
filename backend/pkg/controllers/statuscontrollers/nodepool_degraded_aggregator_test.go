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

package statuscontrollers

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// newTestNodePoolForAggregator builds a minimal HCPOpenShiftClusterNodePool
// suitable for the aggregator tests.
func newTestNodePoolForAggregator(opts ...func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName,
	))
	np := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	for _, opt := range opts {
		opt(np)
	}
	return np
}

func TestNodePoolDegradedAggregator_SyncOnce(t *testing.T) {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName,
	))

	thirtySecondInertia := MustNewInertia(30 * time.Second).Inertia
	fiveMinuteOverrideInertia := MustNewInertia(
		30*time.Second,
		InertiaController{ControllerNameMatcher: regexp.MustCompile(`^SlowController$`), Duration: 5 * time.Minute},
	).Inertia

	// Parent cluster — also needed so the resourcesDBClient is happy when the
	// node-pool CRUD looks up the parent path internally during Replace.
	parentClusterID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	_ = parentClusterID

	tests := []struct {
		name string

		controllers []*api.Controller
		inertia     Inertia
		// initialConditions, if set, is layered onto the node pool before SyncOnce
		// runs. Used to drive the "no-op when conditions unchanged" case.
		initialConditions []metav1.Condition

		expectStatus  metav1.ConditionStatus
		expectReason  string
		expectMessage string
	}{
		{
			name:          "no controllers under the node pool -> Unknown/NoData",
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
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: fine",
		},
		{
			name: "bad controller within 30s inertia stays hidden",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 10*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: boom",
		},
		{
			name: "bad controller past 30s inertia flips aggregate",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 31*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "per-controller override: SlowController stays in inertia window",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "settling", 2*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "SlowController: settling",
		},
		{
			name: "per-controller override: SlowController past 5m flips",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "stuck", 6*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "SlowController_Failed",
			expectMessage: "SlowController: stuck",
		},
		{
			name: "nil inertia propagates immediately",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Second),
			},
			inertia:       nil,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "no-op when conditions unchanged",
			controllers: []*api.Controller{
				controllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			initialConditions: []metav1.Condition{
				{
					Type:    degradedConditionType,
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

			existing := newTestNodePoolForAggregator(func(np *api.HCPOpenShiftClusterNodePool) {
				if len(tc.initialConditions) > 0 {
					np.Status.Conditions = append([]metav1.Condition{}, tc.initialConditions...)
				}
			})
			parentCluster := &api.HCPOpenShiftCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: parentClusterID},
				TrackedResource: arm.TrackedResource{
					Resource: arm.Resource{ID: parentClusterID, Name: testClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, existing}
			for _, ctrl := range tc.controllers {
				seed = append(seed, ctrl)
			}
			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			clock := clocktesting.NewFakePassiveClock(fixedNow)
			syncer := &nodePoolDegradedAggregator{
				cooldownChecker:   alwaysSyncCooldownChecker{},
				nodePoolLister:    &listertesting.SliceNodePoolLister{NodePools: []*api.HCPOpenShiftClusterNodePool{existing}},
				controllerLister:  &listertesting.SliceControllerLister{Controllers: tc.controllers},
				resourcesDBClient: mockDB,
				inertia:           tc.inertia,
				clock:             clock,
				firstObservedBad:  newFirstObservedBadCache(clock),
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, degradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on the node pool")
			assert.Equal(t, tc.expectStatus, cond.Status, "status")
			assert.Equal(t, tc.expectReason, cond.Reason, "reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "message")
		})
	}
}
