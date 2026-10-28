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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

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

// availableTrue and availableFalse represent the allowlisted OIDCClientsDegraded
// condition on ServiceProviderExternalAuth in its healthy ("available") and
// unhealthy ("not available") states, used to prove the aggregator ignores
// SPEA condition changes once the ExternalAuth is deleting.
var (
	availableTrue = metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
		Status:  metav1.ConditionFalse,
		Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected,
		Message: coreapi.ExternalAuthMessageOIDCClientsAllOperational,
	}
	availableFalse = metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
		Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
	}
)

func TestCollectAllowlistedConditions(t *testing.T) {
	pendingUnknown := metav1.Condition{
		Status:  metav1.ConditionUnknown,
		Reason:  coreapi.ExternalAuthUserFacingDegradedReasonEvaluationIncomplete,
		Message: "Waiting for condition to be evaluated",
	}

	tests := []struct {
		name       string
		conditions []metav1.Condition
		// expectReported maps condition Types to their expected Status for reported entries.
		expectReported map[string]metav1.ConditionStatus
		// expectPending lists Types expected to be pending Unknown (not yet evaluated).
		expectPending []string
	}{
		{
			name:       "nil - all pending",
			conditions: nil,
			expectPending: []string{
				coreapi.ExternalAuthOIDCClientsDegradedCondition,
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
			},
		},
		{
			name:       "empty - all pending",
			conditions: []metav1.Condition{},
			expectPending: []string{
				coreapi.ExternalAuthOIDCClientsDegradedCondition,
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
			},
		},
		{
			name: "only non-allowlisted - all pending",
			conditions: []metav1.Condition{
				{Type: "InternalOnly", Status: metav1.ConditionTrue},
			},
			expectPending: []string{
				coreapi.ExternalAuthOIDCClientsDegradedCondition,
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
			},
		},
		{
			name: "one reported False, other pending",
			conditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse},
			},
			expectReported: map[string]metav1.ConditionStatus{coreapi.ExternalAuthOIDCClientsDegradedCondition: metav1.ConditionFalse},
			expectPending:  []string{coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition},
		},
		{
			name: "one reported True, other pending",
			conditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionTrue},
			},
			expectReported: map[string]metav1.ConditionStatus{coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition: metav1.ConditionTrue},
			expectPending:  []string{coreapi.ExternalAuthOIDCClientsDegradedCondition},
		},
		{
			name: "both present - no pending",
			conditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse},
				{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionTrue},
			},
			expectReported: map[string]metav1.ConditionStatus{
				coreapi.ExternalAuthOIDCClientsDegradedCondition:            metav1.ConditionFalse,
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition: metav1.ConditionTrue,
			},
		},
		{
			name: "both present plus non-allowlisted - no pending, non-allowlisted excluded",
			conditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse},
				{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionFalse},
				{Type: "InternalOnly", Status: metav1.ConditionTrue},
			},
			expectReported: map[string]metav1.ConditionStatus{
				coreapi.ExternalAuthOIDCClientsDegradedCondition:            metav1.ConditionFalse,
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition: metav1.ConditionFalse,
			},
		},
	}

	syncer := &externalAuthUserFacingConditionsDegradedAggregator{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := syncer.collectAllowlistedConditions(tc.conditions)
			assert.Len(t, result, len(allowedDegradedConditionTypes), "always returns one entry per allowlisted type")

			byType := make(map[string]metav1.Condition, len(result))
			for _, c := range result {
				byType[c.Type] = c
			}
			for typ, wantStatus := range tc.expectReported {
				got, ok := byType[typ]
				require.True(t, ok, "expected reported condition %s", typ)
				assert.Equal(t, wantStatus, got.Status, "status of %s", typ)
			}
			for _, typ := range tc.expectPending {
				got, ok := byType[typ]
				require.True(t, ok, "expected pending condition %s", typ)
				assert.Equal(t, pendingUnknown.Status, got.Status, "pending %s status", typ)
				assert.Equal(t, pendingUnknown.Reason, got.Reason, "pending %s reason", typ)
				assert.Equal(t, pendingUnknown.Message, got.Message, "pending %s message", typ)
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
			name:         "both contributors False - UserFacing Degraded=False",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageOIDCClientsAllOperational},
					{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageOIDCCACertificateValid},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionFalse),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
			expectMessage:  "All is well",
		},
		{
			name:         "only OIDCClientsDegraded=False, CA absent - Unknown with pending CA message",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageOIDCClientsAllOperational},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionUnknown),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReasonDegradedStateUnknown,
			expectMessage:  coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition + ": Waiting for condition to be evaluated",
		},
		{
			name:         "only CACertificateInvalid=False, OIDCClients absent - Unknown with pending OIDCClients message",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageOIDCCACertificateValid},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionUnknown),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReasonDegradedStateUnknown,
			expectMessage:  coreapi.ExternalAuthOIDCClientsDegradedCondition + ": Waiting for condition to be evaluated",
		},
		{
			name:         "CACertificateInvalid=True, OIDCClients absent - True with both reported and pending messages",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCCACertificateDegradationReason, Message: "1 CA certificate(s) in the issuer bundle have expired: CN=test-ca (NotAfter 2026-01-01T00:00:00Z)"},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionTrue),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": Waiting for condition to be evaluated\n" +
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition + ": 1 CA certificate(s) in the issuer bundle have expired: CN=test-ca (NotAfter 2026-01-01T00:00:00Z)",
		},
		{
			name:         "OIDCClientsDegraded=True, CA absent - True with both reported and pending messages",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionTrue),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret + "\n" +
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition + ": Waiting for condition to be evaluated",
		},
		{
			name:                        "no conditions - Unknown with all pending messages",
			externalAuth:                newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectDegraded:              ptr.To(metav1.ConditionUnknown),
			expectReason:                coreapi.ExternalAuthUserFacingDegradedReasonDegradedStateUnknown,
			expectMessage: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": Waiting for condition to be evaluated\n" +
				coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition + ": Waiting for condition to be evaluated",
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
					{
						Type:   statusutils.DegradedConditionType,
						Status: metav1.ConditionUnknown,
						Reason: coreapi.ExternalAuthUserFacingDegradedReasonDegradedStateUnknown,
						Message: coreapi.ExternalAuthOIDCClientsDegradedCondition + ": Waiting for condition to be evaluated\n" +
							coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition + ": Waiting for condition to be evaluated",
					},
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectNoWrite:               true,
		},
		{
			name: "freezes conditions during deletion — preserves last-known Available",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				ea.Status.UserFacingConditions = []metav1.Condition{availableTrue}
			}),
			// SPEA has a different condition, but the aggregator must not
			// update UserFacingConditions because the resource is deleting.
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{availableFalse}
			}),
			expectNoWrite: true,
		},
		{
			name: "freezes conditions during deletion — preserves empty conditions",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				// No UserFacingConditions were ever written before deletion.
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{availableTrue}
			}),
			expectNoWrite: true,
		},
		{
			name: "freezes conditions during deletion — SPEA already cleaned up",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				ea.Status.UserFacingConditions = []metav1.Condition{availableTrue}
			}),
			// SPEA has been deleted by the child cleanup controller.
			serviceProviderExternalAuth: nil,
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

			cond := apimeta.FindStatusCondition(updatedExternalAuth.Status.UserFacingConditions, statusutils.DegradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on ExternalAuth.UserFacingConditions")
			assert.Equal(t, *tc.expectDegraded, cond.Status, "Degraded condition status")
			assert.Equal(t, tc.expectReason, cond.Reason, "Degraded condition reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "Degraded condition message")
		})
	}
}
