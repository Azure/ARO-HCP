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
func newTestExternalAuthForAggregatorTests(opts ...func(*coreapi.ExternalAuth)) *coreapi.ExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	ea := &coreapi.ExternalAuth{
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

func newTestServiceProviderExternalAuthForAggregator(opts ...func(*coreapi.ServiceProviderExternalAuth)) *coreapi.ServiceProviderExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName +
			"/serviceProviderExternalAuths/" + coreapi.ServiceProviderExternalAuthResourceName,
	))
	serviceProviderExternalAuth := &coreapi.ServiceProviderExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	for _, opt := range opts {
		opt(serviceProviderExternalAuth)
	}
	return serviceProviderExternalAuth
}

func TestCollectAllowlistedSources(t *testing.T) {
	tests := []struct {
		name                                  string
		serviceProviderExternalAuthConditions []metav1.Condition
		expectCount                           int
		expectSourceName                      string
		expectStatus                          metav1.ConditionStatus
		expectReason                          string
		expectMessage                         string
	}{
		{
			name:                                  "no conditions -> zero sources",
			serviceProviderExternalAuthConditions: nil,
			expectCount:                           0,
		},
		{
			name: "all conditions False -> zero sources (not emitted)",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
			},
			expectCount: 0,
		},
		{
			name: "OIDCClientsDegraded=True -> one source",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
			},
			expectCount:      1,
			expectSourceName: coreapi.ExternalAuthOIDCClientsDegradedCondition,
			expectStatus:     metav1.ConditionTrue,
			expectReason:     coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
			expectMessage:    "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
		{
			name: "non-allowlisted condition True is ignored -> zero sources",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: "InternalOnlyCondition", Status: metav1.ConditionTrue, Reason: "SomeReason", Message: "internal details"},
			},
			expectCount: 0,
		},
		{
			name: "allowlisted True plus non-allowlisted True -> only allowlisted emitted",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				{Type: "InternalOnlyCondition", Status: metav1.ConditionTrue, Reason: "SomeReason", Message: "internal details"},
			},
			expectCount:      1,
			expectSourceName: coreapi.ExternalAuthOIDCClientsDegradedCondition,
			expectStatus:     metav1.ConditionTrue,
			expectReason:     coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
			expectMessage:    "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sources := collectAllowlistedSources(tc.serviceProviderExternalAuthConditions)
			assert.Len(t, sources, tc.expectCount, "source count")
			if tc.expectCount > 0 {
				assert.Equal(t, tc.expectSourceName, sources[0].ControllerName, "source name")
				assert.Equal(t, coreapi.ExternalAuthOIDCClientsDegradedCondition, sources[0].Condition.Type, "condition type")
				assert.Equal(t, tc.expectStatus, sources[0].Condition.Status, "condition status")
				assert.Equal(t, tc.expectReason, sources[0].Condition.Reason, "condition reason")
				assert.Equal(t, tc.expectMessage, sources[0].Condition.Message, "condition message")
			}
		})
	}
}

func TestExternalAuthUserFacingDegradedAggregator_SyncOnce(t *testing.T) {
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	tests := []struct {
		name string

		externalAuth                *coreapi.ExternalAuth
		serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth

		expectNoWrite  bool
		expectDegraded *metav1.ConditionStatus
		expectReason   string
		expectMessage  string
	}{
		{
			name:         "ServiceProviderExternalAuth OIDCClientsDegraded=True -> UserFacing OIDCClientsDegraded=True",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuthForAggregator(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				}
			}),
			expectDegraded: ptrTo(metav1.ConditionTrue),
			expectReason:   coreapi.ExternalAuthOIDCClientsDegradedCondition + "_" + coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
			expectMessage:  coreapi.ExternalAuthOIDCClientsDegradedCondition + ": console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
		{
			name:         "ServiceProviderExternalAuth OIDCClientsDegraded=False -> UserFacing OIDCClientsDegraded=False",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuthForAggregator(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
				}
			}),
			expectDegraded: ptrTo(metav1.ConditionFalse),
			expectReason:   "AsExpected",
			expectMessage:  "All is well",
		},
		{
			name:                        "ServiceProviderExternalAuth has no conditions -> UserFacing OIDCClientsDegraded=False",
			externalAuth:                newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuthForAggregator(),
			expectDegraded:              ptrTo(metav1.ConditionFalse),
			expectReason:                "AsExpected",
			expectMessage:               "All is well",
		},
		{
			name:                        "no-op when ServiceProviderExternalAuth not found",
			externalAuth:                newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: nil,
			expectNoWrite:               true,
		},
		{
			name: "no-op when UserFacingConditions already match",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "All is well"},
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuthForAggregator(),
			expectNoWrite:               true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			parentCluster := &coreapi.Cluster{
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

			syncer := &externalAuthUserFacingConditionsDegradedAggregator{
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

			updatedExternalAuth, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).ExternalAuth(statusutils.TestClusterName).Get(ctx, statusutils.TestExternalAuthName)
			require.NoError(t, err)

			if tc.expectNoWrite {
				assert.Equal(t, tc.externalAuth.Status.UserFacingConditions, updatedExternalAuth.Status.UserFacingConditions,
					"UserFacingConditions should not have changed")
				return
			}

			cond := apimeta.FindStatusCondition(updatedExternalAuth.Status.UserFacingConditions, coreapi.ExternalAuthOIDCClientsDegradedCondition)
			require.NotNil(t, cond, "aggregator must set the OIDCClientsDegraded condition on ExternalAuth.UserFacingConditions")
			assert.Equal(t, *tc.expectDegraded, cond.Status, "OIDCClientsDegraded condition status")
			assert.Equal(t, tc.expectReason, cond.Reason, "OIDCClientsDegraded condition reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "OIDCClientsDegraded condition message")
		})
	}
}
