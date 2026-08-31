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

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

// newTestNodePoolForAggregator builds a minimal HCPOpenShiftClusterNodePool
// suitable for the aggregator tests.
func newTestNodePoolForAggregator(opts ...func(*coreapi.HCPOpenShiftClusterNodePool)) *coreapi.HCPOpenShiftClusterNodePool {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/nodePools/" + statusutils.TestNodePoolName,
	))
	np := &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: statusutils.TestNodePoolName,
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
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/nodePools/" + statusutils.TestNodePoolName,
	))

	thirtySecondInertia := statusutils.MustNewInertia(30 * time.Second).Inertia
	fiveMinuteOverrideInertia := statusutils.MustNewInertia(
		30*time.Second,
		statusutils.InertiaController{ControllerNameMatcher: regexp.MustCompile(`^SlowController$`), Duration: 5 * time.Minute},
	).Inertia

	// Parent cluster — also needed so the resourcesDBClient is happy when the
	// node-pool CRUD looks up the parent path internally during Replace.
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	// Collision-safe node-pool desire source names: source prefix + the desire's
	// full lowercased resource ID (see CollectDegradedDesireConditions).
	npApplyName := statusutils.ApplyDesireSourcePrefix + kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
		statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestNodePoolName, "np-apply")
	npReadName := statusutils.ReadDesireSourcePrefix + kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
		statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestNodePoolName, "np-read")

	tests := []struct {
		name string

		controllers []*coreapi.Controller
		// applyDesires / readDesires are exposed to the aggregator via
		// slice-backed listers; only their Degraded=True node-pool-scoped
		// entries for THIS node pool should be folded in.
		applyDesires []*kubeapplierapi.ApplyDesire
		readDesires  []*kubeapplierapi.ReadDesire
		inertia      statusutils.Inertia
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
			name: "all controllers healthy -> no degraded sources -> Unknown/NoData",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			// Healthy controllers are not emitted as sources, so UnionCondition
			// sees zero sources and reports Unknown/NoData ("no data").
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  "NoData",
			expectMessage: "",
		},
		{
			name: "bad controller within 30s inertia stays hidden",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 10*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: boom",
		},
		{
			name: "bad controller past 30s inertia flips aggregate",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 31*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "per-controller override: SlowController stays in inertia window",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "settling", 2*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "SlowController: settling",
		},
		{
			name: "per-controller override: SlowController past 5m flips",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "stuck", 6*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "SlowController_Failed",
			expectMessage: "SlowController: stuck",
		},
		{
			name: "nil inertia propagates immediately",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Second),
			},
			inertia:       nil,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "no-op when conditions unchanged",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			// The single healthy controller yields zero sources -> Unknown/NoData;
			// pre-seeding that exercises the no-op (skip Replace) path.
			initialConditions: []metav1.Condition{
				{
					Type:   statusutils.DegradedConditionType,
					Status: metav1.ConditionUnknown,
					Reason: "NoData",
				},
			},
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  "NoData",
			expectMessage: "",
		},
		{
			name: "degraded node-pool-scoped ApplyDesire past inertia flips the node pool",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			applyDesires: []*kubeapplierapi.ApplyDesire{
				statusutils.NodePoolScopedApplyDesireUnder(parentClusterID, statusutils.TestNodePoolName, "np-apply",
					statusutils.DegradedConditionAged(metav1.ConditionTrue, "Failed", "boom", 1*time.Minute)),
			},
			inertia:      thirtySecondInertia,
			expectStatus: metav1.ConditionTrue,
			// Named by full canonical resource ID; healthy controller omitted from the bad path.
			expectReason:  npApplyName + "_Failed",
			expectMessage: npApplyName + ": boom",
		},
		{
			name: "degraded node-pool-scoped ReadDesire past inertia flips the node pool",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			readDesires: []*kubeapplierapi.ReadDesire{
				statusutils.NodePoolScopedReadDesireUnder(parentClusterID, statusutils.TestNodePoolName, "np-read",
					statusutils.DegradedConditionAged(metav1.ConditionTrue, "Failed", "kaboom", 1*time.Minute)),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  npReadName + "_Failed",
			expectMessage: npReadName + ": kaboom",
		},
		{
			name: "cluster-scoped and other node pools' degraded desires do not affect this node pool",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			applyDesires: []*kubeapplierapi.ApplyDesire{
				// Cluster-scoped desire -> not returned by ListForNodePool.
				statusutils.ApplyDesireUnder(parentClusterID, "cluster-apply",
					statusutils.DegradedConditionAged(metav1.ConditionTrue, "Failed", "cluster boom", 1*time.Minute)),
				// A different node pool's desire -> not returned by ListForNodePool.
				statusutils.NodePoolScopedApplyDesireUnder(parentClusterID, "other-nodepool", "other-apply",
					statusutils.DegradedConditionAged(metav1.ConditionTrue, "Failed", "other boom", 1*time.Minute)),
			},
			inertia: thirtySecondInertia,
			// None of the seeded desires belong to this node pool, and the
			// controller is healthy, so there are no sources -> Unknown/NoData.
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  "NoData",
			expectMessage: "",
		},
		{
			name: "healthy and condition-less node-pool desires contribute nothing",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Minute),
			},
			applyDesires: []*kubeapplierapi.ApplyDesire{
				statusutils.NodePoolScopedApplyDesireUnder(parentClusterID, statusutils.TestNodePoolName, "np-ok",
					statusutils.DegradedConditionAged(metav1.ConditionFalse, "NoErrors", "all good", 1*time.Minute)),
				statusutils.NodePoolScopedApplyDesireUnder(parentClusterID, statusutils.TestNodePoolName, "np-none"),
			},
			inertia:      thirtySecondInertia,
			expectStatus: metav1.ConditionTrue,
			// Only the degraded controller is reported; the healthy/condition-less
			// desires contribute nothing.
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			existing := newTestNodePoolForAggregator(func(np *coreapi.HCPOpenShiftClusterNodePool) {
				if len(tc.initialConditions) > 0 {
					np.Status.Conditions = append([]metav1.Condition{}, tc.initialConditions...)
				}
			})
			parentCluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, existing}
			for _, ctrl := range tc.controllers {
				seed = append(seed, ctrl)
			}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			clock := clocktesting.NewFakePassiveClock(statusutils.FixedNow)
			syncer := &nodePoolDegradedAggregator{
				nodePoolLister:    &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockDB},
				controllerLister:  &corelistertesting.DBControllerLister{ResourcesDBClient: mockDB},
				resourcesDBClient: mockDB,
				inertia:           tc.inertia,
				clock:             clock,
				firstObservedBad:  statusutils.NewFirstObservedBadCache(clock),
				applyDesireLister: &kubeapplierlistertesting.SliceApplyDesireLister{Desires: tc.applyDesires},
				readDesireLister:  &kubeapplierlistertesting.SliceReadDesireLister{Desires: tc.readDesires},
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPNodePoolKey{
				SubscriptionID:    statusutils.TestSubscriptionID,
				ResourceGroupName: statusutils.TestResourceGroupName,
				HCPClusterName:    statusutils.TestClusterName,
				HCPNodePoolName:   statusutils.TestNodePoolName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).NodePools(statusutils.TestClusterName).Get(ctx, statusutils.TestNodePoolName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, statusutils.DegradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on the node pool")
			assert.Equal(t, tc.expectStatus, cond.Status, "status")
			assert.Equal(t, tc.expectReason, cond.Reason, "reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "message")
		})
	}
}
