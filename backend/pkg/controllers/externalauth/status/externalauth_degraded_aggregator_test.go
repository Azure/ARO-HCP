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
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/statusutil"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// newTestExternalAuthForAggregator builds a minimal
// HCPOpenShiftClusterExternalAuth suitable for the aggregator tests.
func newTestExternalAuthForAggregator(opts ...func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutil.TestSubscriptionID +
			"/resourceGroups/" + statusutil.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutil.TestClusterName +
			"/externalAuths/" + statusutil.TestExternalAuthName,
	))
	ea := &api.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: statusutil.TestExternalAuthName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	for _, opt := range opts {
		opt(ea)
	}
	return ea
}

func TestExternalAuthDegradedAggregator_SyncOnce(t *testing.T) {
	parentResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutil.TestSubscriptionID +
			"/resourceGroups/" + statusutil.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutil.TestClusterName +
			"/externalAuths/" + statusutil.TestExternalAuthName,
	))
	parentClusterID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutil.TestSubscriptionID +
			"/resourceGroups/" + statusutil.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutil.TestClusterName,
	))

	thirtySecondInertia := statusutil.MustNewInertia(30 * time.Second).Inertia
	fiveMinuteOverrideInertia := statusutil.MustNewInertia(
		30*time.Second,
		statusutil.InertiaController{ControllerNameMatcher: regexp.MustCompile(`^SlowController$`), Duration: 5 * time.Minute},
	).Inertia

	tests := []struct {
		name string

		controllers []*api.Controller
		inertia     statusutil.Inertia
		// initialConditions, if set, is layered onto the external auth before
		// SyncOnce runs.
		initialConditions []metav1.Condition

		expectStatus  metav1.ConditionStatus
		expectReason  string
		expectMessage string
	}{
		{
			name:          "no controllers under the external auth -> Unknown/NoData",
			controllers:   nil,
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  "NoData",
			expectMessage: "",
		},
		{
			name: "all-good aggregate",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: fine",
		},
		{
			name: "bad controller within 30s inertia stays hidden",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 5*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: boom",
		},
		{
			name: "bad controller past 30s inertia flips aggregate",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Minute),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "per-controller override delays SlowController",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "settling", 2*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "SlowController: settling",
		},
		{
			name: "per-controller override: SlowController past 5m flips",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "stuck", 6*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "SlowController_Failed",
			expectMessage: "SlowController: stuck",
		},
		{
			name: "nil inertia propagates immediately",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Second),
			},
			inertia:       nil,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "no-op when conditions unchanged",
			controllers: []*api.Controller{
				statusutil.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			initialConditions: []metav1.Condition{
				{
					Type:    statusutil.DegradedConditionType,
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

			existing := newTestExternalAuthForAggregator(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				if len(tc.initialConditions) > 0 {
					ea.Status.Conditions = append([]metav1.Condition{}, tc.initialConditions...)
				}
			})
			parentCluster := &api.HCPOpenShiftCluster{
				CosmosMetadata: arm.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: arm.TrackedResource{
					Resource: arm.Resource{ID: parentClusterID, Name: statusutil.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, existing}
			for _, ctrl := range tc.controllers {
				seed = append(seed, ctrl)
			}
			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			clock := clocktesting.NewFakePassiveClock(statusutil.FixedNow)
			syncer := &externalAuthDegradedAggregator{
				externalAuthLister: &listertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				controllerLister:   &listertesting.DBControllerLister{ResourcesDBClient: mockDB},
				resourcesDBClient:  mockDB,
				inertia:            tc.inertia,
				clock:              clock,
				firstObservedBad:   statusutil.NewFirstObservedBadCache(clock),
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutil.TestSubscriptionID,
				ResourceGroupName:   statusutil.TestResourceGroupName,
				HCPClusterName:      statusutil.TestClusterName,
				HCPExternalAuthName: statusutil.TestExternalAuthName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(statusutil.TestSubscriptionID, statusutil.TestResourceGroupName).ExternalAuth(statusutil.TestClusterName).Get(ctx, statusutil.TestExternalAuthName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, statusutil.DegradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on the external auth")
			assert.Equal(t, tc.expectStatus, cond.Status, "status")
			assert.Equal(t, tc.expectReason, cond.Reason, "reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "message")
		})
	}
}
