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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

// newTestExternalAuthForAggregator builds a minimal ExternalAuth for aggregator tests.
func newTestExternalAuthForAggregatorTests(opts ...func(*coreapi.HCPOpenShiftClusterExternalAuth)) *coreapi.HCPOpenShiftClusterExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	ea := &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: statusutils.TestExternalAuthName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	for _, opt := range opts {
		opt(ea)
	}
	return ea
}

func newTestSPEAForAggregator(opts ...func(*coreapi.ServiceProviderExternalAuth)) *coreapi.ServiceProviderExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName +
			"/serviceProviderExternalAuths/" + coreapi.ServiceProviderExternalAuthResourceName,
	))
	spea := &coreapi.ServiceProviderExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	for _, opt := range opts {
		opt(spea)
	}
	return spea
}

func TestAggregateDegradedCondition(t *testing.T) {
	tests := []struct {
		name                                  string
		serviceProviderExternalAuthConditions []metav1.Condition
		expectStatus                          metav1.ConditionStatus
		expectReason                          string
		expectMessage                         string
	}{
		{
			name:                                  "no conditions -> Degraded=False",
			serviceProviderExternalAuthConditions: nil,
			expectStatus:                          metav1.ConditionFalse,
			expectReason:                          coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
			expectMessage:                         coreapi.ExternalAuthMessageAllOperational,
		},
		{
			name: "all conditions False -> Degraded=False",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
			expectMessage: coreapi.ExternalAuthMessageAllOperational,
		},
		{
			name: "OIDCClientsDegraded=True -> Degraded=True with prefixed message",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console: " + coreapi.ExternalAuthMessageAwaitingSecret},
			},
			expectStatus:  metav1.ConditionTrue,
			expectReason:  coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
		{
			name: "multiple True conditions -> merged message",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				{Type: "FutureCondition", Status: metav1.ConditionTrue, Reason: "SomeReason", Message: "something else"},
			},
			expectStatus:  metav1.ConditionTrue,
			expectReason:  coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": console: " + coreapi.ExternalAuthMessageAwaitingSecret + "\nFutureCondition: something else",
		},
		{
			name: "mixed True and False -> Degraded=True with only True in message",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "cli: " + coreapi.ExternalAuthMessageIssuerURLInvalid},
				{Type: "FutureCondition", Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "all good"},
			},
			expectStatus:  metav1.ConditionTrue,
			expectReason:  coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": cli: " + coreapi.ExternalAuthMessageIssuerURLInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateDegradedCondition(tc.serviceProviderExternalAuthConditions)
			assert.Equal(t, statusutils.DegradedConditionType, got.Type, "condition type")
			assert.Equal(t, tc.expectStatus, got.Status, "condition status")
			assert.Equal(t, tc.expectReason, got.Reason, "condition reason")
			assert.Equal(t, tc.expectMessage, got.Message, "condition message")
		})
	}
}

func TestExternalAuthUserFacingConditionsAggregator_SyncOnce(t *testing.T) {
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	tests := []struct {
		name string

		externalAuth                *coreapi.HCPOpenShiftClusterExternalAuth
		serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth

		expectNoWrite  bool
		expectDegraded *metav1.ConditionStatus
		expectReason   string
		expectMessage  string
	}{
		{
			name:         "ServiceProviderExternalAuth OIDCClientsDegraded=True -> UserFacing Degraded=True",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestSPEAForAggregator(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				}
			}),
			expectDegraded: ptrTo(metav1.ConditionTrue),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage:  coreapi.ExternalAuthOIDCClientsDegradedCondition + ": console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
		{
			name:         "ServiceProviderExternalAuth OIDCClientsDegraded=False -> UserFacing Degraded=False",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestSPEAForAggregator(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
				}
			}),
			expectDegraded: ptrTo(metav1.ConditionFalse),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
			expectMessage:  coreapi.ExternalAuthMessageAllOperational,
		},
		{
			name:                        "ServiceProviderExternalAuth has no conditions -> UserFacing Degraded=False",
			externalAuth:                newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestSPEAForAggregator(),
			expectDegraded:              ptrTo(metav1.ConditionFalse),
			expectReason:                coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
			expectMessage:               coreapi.ExternalAuthMessageAllOperational,
		},
		{
			name:                        "no-op when ServiceProviderExternalAuth not found",
			externalAuth:                newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: nil,
			expectNoWrite:               true,
		},
		{
			name: "no-op when UserFacingConditions already match",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{
					{Type: statusutils.DegradedConditionType, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthUserFacingDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
				}
			}),
			serviceProviderExternalAuth: newTestSPEAForAggregator(),
			expectNoWrite:               true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			parentCluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, tc.externalAuth}
			if tc.serviceProviderExternalAuth != nil {
				seed = append(seed, tc.serviceProviderExternalAuth)
			}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			syncer := &externalAuthUserFacingConditionsAggregator{
				externalAuthLister:                &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				serviceProviderExternalAuthLister: &corelistertesting.DBServiceProviderExternalAuthLister{ResourcesDBClient: mockDB},
				resourcesDBClient:                 mockDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			updatedEA, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).ExternalAuth(statusutils.TestClusterName).Get(ctx, statusutils.TestExternalAuthName)
			require.NoError(t, err)

			if tc.expectNoWrite {
				assert.Equal(t, tc.externalAuth.Status.UserFacingConditions, updatedEA.Status.UserFacingConditions,
					"UserFacingConditions should not have changed")
				return
			}

			cond := apimeta.FindStatusCondition(updatedEA.Status.UserFacingConditions, statusutils.DegradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on ExternalAuth.UserFacingConditions")
			assert.Equal(t, *tc.expectDegraded, cond.Status, "Degraded condition status")
			assert.Equal(t, tc.expectReason, cond.Reason, "Degraded condition reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "Degraded condition message")
		})
	}
}
