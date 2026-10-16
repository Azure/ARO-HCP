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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
		Message: coreapi.ExternalAuthMessageAllOperational,
	}
	availableFalse = metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
		Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
	}
)

func TestCollectAllowlisted(t *testing.T) {
	tests := []struct {
		name                                  string
		serviceProviderExternalAuthConditions []metav1.Condition
		expectCount                           int
		expectType                            string
		expectStatus                          metav1.ConditionStatus
		expectReason                          string
		expectMessage                         string
	}{
		{
			name:                                  "no conditions -> zero results",
			serviceProviderExternalAuthConditions: nil,
			expectCount:                           0,
		},
		{
			name: "OIDCClientsDegraded=False -> one result (kept with original status)",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
			},
			expectCount:  1,
			expectType:   coreapi.ExternalAuthOIDCClientsDegradedCondition,
			expectStatus: metav1.ConditionFalse,
			expectReason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected,
		},
		{
			name: "OIDCClientsDegraded=True -> one result",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
			},
			expectCount:   1,
			expectType:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
			expectMessage: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
		{
			name: "non-allowlisted condition is ignored -> zero results",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: "InternalOnlyCondition", Status: metav1.ConditionTrue, Reason: "SomeReason", Message: "internal details"},
			},
			expectCount: 0,
		},
		{
			name: "allowlisted plus non-allowlisted -> only allowlisted returned",
			serviceProviderExternalAuthConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				{Type: "InternalOnlyCondition", Status: metav1.ConditionTrue, Reason: "SomeReason", Message: "internal details"},
			},
			expectCount:   1,
			expectType:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
			expectMessage: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
	}

	syncer := &externalAuthUserFacingConditionsDegradedAggregator{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := syncer.collectAllowlisted(tc.serviceProviderExternalAuthConditions)
			assert.Len(t, result, tc.expectCount, "result count")
			if tc.expectCount > 0 {
				assert.Equal(t, tc.expectType, result[0].Type, "condition type")
				assert.Equal(t, tc.expectStatus, result[0].Status, "condition status")
				assert.Equal(t, tc.expectReason, result[0].Reason, "condition reason")
				if tc.expectMessage != "" {
					assert.Equal(t, tc.expectMessage, result[0].Message, "condition message")
				}
			}
		})
	}
}

func TestHasAllowlistedCondition(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		expect     bool
	}{
		{
			name:       "nil -> false",
			conditions: nil,
			expect:     false,
		},
		{
			name:       "empty -> false",
			conditions: []metav1.Condition{},
			expect:     false,
		},
		{
			name: "only non-allowlisted -> false",
			conditions: []metav1.Condition{
				{Type: "InternalOnly", Status: metav1.ConditionTrue},
			},
			expect: false,
		},
		{
			name: "has allowlisted -> true",
			conditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse},
			},
			expect: true,
		},
	}

	syncer := &externalAuthUserFacingConditionsDegradedAggregator{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, syncer.hasAllowlistedCondition(tc.conditions))
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
			name:         "ServiceProviderExternalAuth OIDCClientsDegraded=True -> UserFacing Degraded=True",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation, Message: "console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionTrue),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReason,
			expectMessage:  coreapi.ExternalAuthOIDCClientsDegradedCondition + ": console/openshift-console: " + coreapi.ExternalAuthMessageAwaitingSecret,
		},
		{
			name:         "ServiceProviderExternalAuth OIDCClientsDegraded=False -> UserFacing Degraded=False",
			externalAuth: newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				serviceProviderExternalAuth.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageAllOperational},
				}
			}),
			expectDegraded: ptr.To(metav1.ConditionFalse),
			expectReason:   coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
			expectMessage:  "All is well",
		},
		{
			name:                        "ServiceProviderExternalAuth has no conditions -> UserFacing Degraded=Unknown",
			externalAuth:                newTestExternalAuthForAggregatorTests(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectDegraded:              ptr.To(metav1.ConditionUnknown),
			expectReason:                coreapi.ExternalAuthUserFacingDegradedReasonEvaluationIncomplete,
			expectMessage:               "Waiting for conditions to be evaluated",
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
					{Type: statusutils.DegradedConditionType, Status: metav1.ConditionUnknown, Reason: coreapi.ExternalAuthUserFacingDegradedReasonEvaluationIncomplete, Message: "Waiting for conditions to be evaluated"},
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
				degradedGauge: prometheus.NewGaugeVec(
					prometheus.GaugeOpts{Name: "test_backend_externalauth_degraded"},
					[]string{"resource_id", "controller"},
				),
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

// TestExternalAuthUserFacingDegradedAggregator_SyncOnce_DegradedGauge verifies that the
// backend_externalauth_degraded gauge reflects the user-facing Degraded condition:
// 1 when Degraded=True, 0 otherwise.
func TestExternalAuthUserFacingDegradedAggregator_SyncOnce_DegradedGauge(t *testing.T) {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))
	resourceID := strings.ToLower(parentResourceID.String())

	tests := []struct {
		name                              string
		serviceProviderExternalAuthStatus metav1.ConditionStatus
		expectGaugeValue                  float64
	}{
		{
			name:                              "OIDCClientsDegraded=True → gauge 1",
			serviceProviderExternalAuthStatus: metav1.ConditionTrue,
			expectGaugeValue:                  1,
		},
		{
			name:                              "OIDCClientsDegraded=False → gauge 0",
			serviceProviderExternalAuthStatus: metav1.ConditionFalse,
			expectGaugeValue:                  0,
		},
		{
			name:                              "OIDCClientsDegraded=Unknown → gauge 0",
			serviceProviderExternalAuthStatus: metav1.ConditionUnknown,
			expectGaugeValue:                  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			externalAuth := newTestExternalAuthForAggregatorTests()
			serviceProviderExternalAuth := newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{
					{Type: coreapi.ExternalAuthOIDCClientsDegradedCondition, Status: tc.serviceProviderExternalAuthStatus, Reason: "TestReason", Message: "test"},
				}
			})

			parentCluster := &coreapi.Cluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, externalAuth, serviceProviderExternalAuth}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			reg := prometheus.NewRegistry()
			degradedGauge := prometheus.NewGaugeVec(
				prometheus.GaugeOpts{Name: "backend_externalauth_degraded"},
				[]string{"resource_id", "controller"},
			)
			reg.MustRegister(degradedGauge)

			syncer := &externalAuthUserFacingConditionsDegradedAggregator{
				externalAuthLister:                &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				serviceProviderExternalAuthLister: &corelistertesting.DBServiceProviderExternalAuthLister{ResourcesDBClient: mockDB},
				resourcesDBClient:                 mockDB,
				degradedGauge:                     degradedGauge,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			expected := fmt.Sprintf(`# HELP backend_externalauth_degraded 
# TYPE backend_externalauth_degraded gauge
backend_externalauth_degraded{controller="%s",resource_id="%s"} %v
`, ExternalAuthUserFacingConditionsDegradedAggregatorControllerName, resourceID, tc.expectGaugeValue)
			require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_externalauth_degraded"))
		})
	}
}

// TestExternalAuthUserFacingDegradedAggregator_SyncOnce_NotFound_SetsGaugeToZero verifies that
// when the ExternalAuth is NotFound, SyncOnce sets the gauge to 0 (so a stale degraded=1 doesn't linger).
func TestExternalAuthUserFacingDegradedAggregator_SyncOnce_NotFound_SetsGaugeToZero(t *testing.T) {
	ctx := context.Background()
	resourceID := strings.ToLower("/subscriptions/" + statusutils.TestSubscriptionID +
		"/resourcegroups/" + statusutils.TestResourceGroupName +
		"/providers/microsoft.redhatopenshift/hcpopenshiftclusters/" + statusutils.TestClusterName +
		"/externalauths/" + statusutils.TestExternalAuthName)

	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	reg := prometheus.NewRegistry()
	degradedGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "backend_externalauth_degraded"},
		[]string{"resource_id", "controller"},
	)
	reg.MustRegister(degradedGauge)

	// Seed DB with only the parent cluster (no ExternalAuth)
	parentCluster := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   parentClusterID,
			PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
		},
	}
	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{parentCluster})
	require.NoError(t, err)

	syncer := &externalAuthUserFacingConditionsDegradedAggregator{
		externalAuthLister:                &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
		serviceProviderExternalAuthLister: &corelistertesting.DBServiceProviderExternalAuthLister{ResourcesDBClient: mockDB},
		resourcesDBClient:                 mockDB,
		degradedGauge:                     degradedGauge,
	}

	// Set the gauge to 1 (simulating a previously degraded ExternalAuth)
	degradedGauge.With(prometheus.Labels{
		"resource_id": resourceID,
		"controller":  ExternalAuthUserFacingConditionsDegradedAggregatorControllerName,
	}).Set(1)

	// Verify gauge is set to 1
	expected := fmt.Sprintf(`# HELP backend_externalauth_degraded 
# TYPE backend_externalauth_degraded gauge
backend_externalauth_degraded{controller="%s",resource_id="%s"} 1
`, ExternalAuthUserFacingConditionsDegradedAggregatorControllerName, resourceID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected), "backend_externalauth_degraded"))

	// Call SyncOnce — ExternalAuth is NotFound
	err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
		SubscriptionID:      statusutils.TestSubscriptionID,
		ResourceGroupName:   statusutils.TestResourceGroupName,
		HCPClusterName:      statusutils.TestClusterName,
		HCPExternalAuthName: statusutils.TestExternalAuthName,
	})
	require.NoError(t, err)

	// Verify gauge is now 0 (not deleted, just set to 0)
	expectedZero := fmt.Sprintf(`# HELP backend_externalauth_degraded 
# TYPE backend_externalauth_degraded gauge
backend_externalauth_degraded{controller="%s",resource_id="%s"} 0
`, ExternalAuthUserFacingConditionsDegradedAggregatorControllerName, resourceID)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expectedZero), "backend_externalauth_degraded"))
}
