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

package validationutils

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	propagationTestTenantID       = "11111111-1111-1111-1111-111111111111"
	propagationTestSubscriptionID = "00000000-0000-0000-0000-000000000000"
	propagationTestPrincipalID    = "22222222-2222-2222-2222-222222222222"
)

// newRoleAssignmentsPager returns a pager that yields each element of pages as one page, or fails
// with err on the first fetch when err is non-nil.
func newRoleAssignmentsPager(err error, pages ...[]*armauthorization.RoleAssignment) *runtime.Pager[armauthorization.RoleAssignmentsClientListForScopeResponse] {
	index := 0
	return runtime.NewPager(runtime.PagingHandler[armauthorization.RoleAssignmentsClientListForScopeResponse]{
		More: func(armauthorization.RoleAssignmentsClientListForScopeResponse) bool {
			return index < len(pages)
		},
		Fetcher: func(context.Context, *armauthorization.RoleAssignmentsClientListForScopeResponse) (armauthorization.RoleAssignmentsClientListForScopeResponse, error) {
			if err != nil {
				return armauthorization.RoleAssignmentsClientListForScopeResponse{}, err
			}
			var page []*armauthorization.RoleAssignment
			if index < len(pages) {
				page = pages[index]
			}
			index++
			return armauthorization.RoleAssignmentsClientListForScopeResponse{
				RoleAssignmentListResult: armauthorization.RoleAssignmentListResult{Value: page},
			}, nil
		},
	})
}

func newTestRoleAssignment(id string, scope string, principalID string, createdOn time.Time) *armauthorization.RoleAssignment {
	return &armauthorization.RoleAssignment{
		ID: ptr.To(id),
		Properties: &armauthorization.RoleAssignmentProperties{
			Scope:       ptr.To(scope),
			PrincipalID: ptr.To(principalID),
			CreatedOn:   ptr.To(createdOn),
		},
	}
}

func TestScopeCovers(t *testing.T) {
	const subnet = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
	tests := []struct {
		name            string
		assignmentScope string
		resourceScope   string
		want            bool
	}{
		{name: "same scope", assignmentScope: subnet, resourceScope: subnet, want: true},
		{name: "same scope different case", assignmentScope: strings.ToUpper(subnet), resourceScope: subnet, want: true},
		{name: "parent resource", assignmentScope: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet", resourceScope: subnet, want: true},
		{name: "resource group", assignmentScope: "/subscriptions/sub/resourceGroups/rg", resourceScope: subnet, want: true},
		{name: "subscription", assignmentScope: "/subscriptions/sub", resourceScope: subnet, want: true},
		{name: "root", assignmentScope: "/", resourceScope: subnet, want: true},
		{name: "management group", assignmentScope: "/providers/Microsoft.Management/managementGroups/mg", resourceScope: subnet, want: true},
		{name: "child resource does not cover parent", assignmentScope: subnet, resourceScope: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet", want: false},
		{name: "sibling resource group with shared prefix", assignmentScope: "/subscriptions/sub/resourceGroups/rg2", resourceScope: subnet, want: false},
		{name: "unrelated subscription", assignmentScope: "/subscriptions/other", resourceScope: subnet, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scopeCovers(tt.assignmentScope, tt.resourceScope))
		})
	}
}

func TestIsRecentCoveringAssignment(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const scope = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg"
	window := 30 * time.Minute
	tests := []struct {
		name       string
		assignment *armauthorization.RoleAssignment
		want       bool
	}{
		{name: "nil assignment", assignment: nil, want: false},
		{name: "nil properties", assignment: &armauthorization.RoleAssignment{}, want: false},
		{name: "recent at resource group", assignment: newTestRoleAssignment("ra", "/subscriptions/sub/resourceGroups/rg", propagationTestPrincipalID, now.Add(-5*time.Minute)), want: true},
		{name: "principal ID case-insensitive", assignment: newTestRoleAssignment("ra", scope, strings.ToUpper(propagationTestPrincipalID), now.Add(-5*time.Minute)), want: true},
		{name: "exactly at window edge", assignment: newTestRoleAssignment("ra", scope, propagationTestPrincipalID, now.Add(-window)), want: true},
		{name: "older than window", assignment: newTestRoleAssignment("ra", scope, propagationTestPrincipalID, now.Add(-window-time.Second)), want: false},
		{name: "small clock skew into the future", assignment: newTestRoleAssignment("ra", scope, propagationTestPrincipalID, now.Add(30*time.Second)), want: true},
		{name: "far in the future", assignment: newTestRoleAssignment("ra", scope, propagationTestPrincipalID, now.Add(time.Hour)), want: false},
		{name: "different principal", assignment: newTestRoleAssignment("ra", scope, "other", now.Add(-time.Minute)), want: false},
		{name: "child scope", assignment: newTestRoleAssignment("ra", scope+"/securityRules/rule", propagationTestPrincipalID, now.Add(-time.Minute)), want: false},
		{
			name: "missing createdOn",
			assignment: &armauthorization.RoleAssignment{Properties: &armauthorization.RoleAssignmentProperties{
				Scope: ptr.To(scope), PrincipalID: ptr.To(propagationTestPrincipalID),
			}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRecentCoveringAssignment(tt.assignment, scope, propagationTestPrincipalID, now, window))
		})
	}
}

func TestFailureWithPropagationGuidance(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nsgID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + propagationTestSubscriptionID + "/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg"))
	subnetID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + propagationTestSubscriptionID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"))
	identityID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + propagationTestSubscriptionID + "/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/operator"))
	principalIDs := map[string]string{strings.ToLower(identityID.String()): propagationTestPrincipalID}

	notAllowed := []*checkaccessv2AuthorizationDecisionData{{ActionID: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.NotAllowed}}
	denied := []*checkaccessv2AuthorizationDecisionData{{ActionID: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Denied}}
	recentRGAssignment := newTestRoleAssignment("recent-rg", "/subscriptions/"+propagationTestSubscriptionID+"/resourceGroups/rg", propagationTestPrincipalID, now.Add(-2*time.Minute))
	oldRGAssignment := newTestRoleAssignment("old-rg", "/subscriptions/"+propagationTestSubscriptionID+"/resourceGroups/rg", propagationTestPrincipalID, now.Add(-3*time.Hour))
	expiredRGAssignment := newTestRoleAssignment("expired-rg", "/subscriptions/"+propagationTestSubscriptionID+"/resourceGroups/rg", propagationTestPrincipalID, now.Add(-90*time.Minute))
	forbidden := &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"}
	recentNSGAssignment := newTestRoleAssignment("recent-nsg", nsgID.String(), propagationTestPrincipalID, now.Add(-1*time.Minute))
	slowNSGAssignment := newTestRoleAssignment("slow-nsg", nsgID.String(), propagationTestPrincipalID, now.Add(-50*time.Minute))

	tests := []struct {
		name               string
		nilDetector        bool
		missing            []*identityResourceMissingPermissions
		principalIDs       map[string]string
		setupMocks         func(*azureclient.MockFirstPartyApplicationClientBuilder, *azureclient.MockRoleAssignmentsClient)
		wantGuidance       bool
		wantLookupErrors   float64
		wantForbidden      float64
		wantWindowExpired  float64
		wantInternalSubstr string
		wantUserSubstr     string
	}{
		{
			name:        "nil detector never adds guidance",
			nilDetector: true,
			missing:     []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
		},
		{
			name:    "explicit deny never gets guidance",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: denied}},
		},
		{
			name:         "unknown principal never gets guidance",
			missing:      []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			principalIDs: map[string]string{},
		},
		{
			name:    "recent inherited assignment gets guidance",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).DoAndReturn(
					func(_ string, options *armauthorization.RoleAssignmentsClientListForScopeOptions) *runtime.Pager[armauthorization.RoleAssignmentsClientListForScopeResponse] {
						assert.Equal(t, "principalId eq '"+propagationTestPrincipalID+"'", ptr.Deref(options.Filter, ""))
						return newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{oldRGAssignment}, []*armauthorization.RoleAssignment{recentRGAssignment, recentNSGAssignment})
					})
			},
			wantGuidance:       true,
			wantInternalSubstr: "recent-nsg",
			// The newest covering assignment, not the oldest, anchors the customer's wait.
			wantUserSubstr: "most recently at 2026-01-01T11:59:00Z",
		},
		{
			name:    "assignment within the observed propagation tail gets guidance",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{slowNSGAssignment}))
			},
			wantGuidance:       true,
			wantInternalSubstr: "slow-nsg",
			wantUserSubstr:     "most recently at 2026-01-01T11:10:00Z",
		},
		{
			name:    "only old assignments get no guidance",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{oldRGAssignment}))
			},
		},
		{
			name:    "assignment just past the window fails and counts window expiry",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{oldRGAssignment, expiredRGAssignment}))
			},
			wantWindowExpired: 1,
		},
		{
			name: "window expiry counts when other resources have recent assignments",
			missing: []*identityResourceMissingPermissions{
				{Resource: nsgID, Identity: identityID, Decisions: notAllowed},
				{Resource: subnetID, Identity: identityID, Decisions: notAllowed},
			},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil).Times(1)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{recentNSGAssignment}))
				client.EXPECT().NewListForScopePager(subnetID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{expiredRGAssignment}))
			},
			wantWindowExpired: 1,
		},
		{
			name: "window expiry is not counted when a resource has no covering assignment",
			missing: []*identityResourceMissingPermissions{
				{Resource: nsgID, Identity: identityID, Decisions: notAllowed},
				{Resource: subnetID, Identity: identityID, Decisions: notAllowed},
			},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil).Times(1)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{expiredRGAssignment}))
				client.EXPECT().NewListForScopePager(subnetID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{oldRGAssignment}))
			},
		},
		{
			name:    "unauthorized list keeps the failure and is counted",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(forbidden, nil))
			},
			wantForbidden: 1,
		},
		{
			name: "every missing resource must be covered",
			missing: []*identityResourceMissingPermissions{
				{Resource: nsgID, Identity: identityID, Decisions: notAllowed},
				{Resource: subnetID, Identity: identityID, Decisions: notAllowed},
			},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil).Times(1)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{recentNSGAssignment}))
				client.EXPECT().NewListForScopePager(subnetID.String(), gomock.Any()).Return(newRoleAssignmentsPager(nil, []*armauthorization.RoleAssignment{recentNSGAssignment}))
			},
		},
		{
			name:    "client build failure keeps the plain failure and is counted",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, _ *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(nil, errors.New("no credential"))
			},
			wantLookupErrors: 1,
		},
		{
			name:    "list failure keeps the plain failure and is counted",
			missing: []*identityResourceMissingPermissions{{Resource: nsgID, Identity: identityID, Decisions: notAllowed}},
			setupMocks: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, client *azureclient.MockRoleAssignmentsClient) {
				builder.EXPECT().RoleAssignmentsClient(propagationTestTenantID, propagationTestSubscriptionID).Return(client, nil)
				client.EXPECT().NewListForScopePager(nsgID.String(), gomock.Any()).Return(newRoleAssignmentsPager(errors.New("connection reset"), nil))
			},
			wantLookupErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			client := azureclient.NewMockRoleAssignmentsClient(ctrl)
			if tt.setupMocks != nil {
				tt.setupMocks(builder, client)
			}
			var detector *RoleAssignmentPropagationDetector
			if !tt.nilDetector {
				detector = NewRoleAssignmentPropagationDetector(builder, DefaultRoleAssignmentPropagationWindow, clocktesting.NewFakePassiveClock(now))
			}
			ids := principalIDs
			if tt.principalIDs != nil {
				ids = tt.principalIDs
			}
			validationName := "Test" + strings.ReplaceAll(tt.name, " ", "")

			const missingPermissionsMessage = "Operators missing required permissions: [...]"

			detectionsBefore := testutil.ToFloat64(roleAssignmentPropagationDetections.WithLabelValues(validationName))
			lookupErrorsBefore := testutil.ToFloat64(roleAssignmentPropagationLookupErrors.WithLabelValues(validationName))
			lookupForbiddenBefore := testutil.ToFloat64(roleAssignmentPropagationLookupForbidden.WithLabelValues(validationName))
			windowExpiredBefore := testutil.ToFloat64(roleAssignmentPropagationWindowExpired.WithLabelValues(validationName))
			result, gotGuidance := failureWithPropagationGuidance(context.Background(), detector, validationName, propagationTestTenantID, tt.missing, ids, missingPermissionsMessage)

			assert.Equal(t, tt.wantGuidance, gotGuidance)
			assert.Equal(t, lookupErrorsBefore+tt.wantLookupErrors, testutil.ToFloat64(roleAssignmentPropagationLookupErrors.WithLabelValues(validationName)))
			assert.Equal(t, lookupForbiddenBefore+tt.wantForbidden, testutil.ToFloat64(roleAssignmentPropagationLookupForbidden.WithLabelValues(validationName)))
			assert.Equal(t, windowExpiredBefore+tt.wantWindowExpired, testutil.ToFloat64(roleAssignmentPropagationWindowExpired.WithLabelValues(validationName)))
			if !tt.wantGuidance {
				assert.Equal(t, detectionsBefore, testutil.ToFloat64(roleAssignmentPropagationDetections.WithLabelValues(validationName)))
				return
			}
			require.NoError(t, result.Validate())
			// Propagation guidance changes the message, never the outcome: the operation must
			// still fail on the usual schedule.
			assert.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
			assert.Equal(t, missingPermissionsPendingPropagationReason, result.Reason())
			assert.Contains(t, result.InternalMessage(), tt.wantInternalSubstr)
			userMessage := result.Outcome.Failed.UserMessage
			assert.Contains(t, userMessage, missingPermissionsMessage, "the customer still needs to know which permissions are missing")
			assert.Contains(t, userMessage, tt.wantUserSubstr)
			assert.Contains(t, userMessage, "retry the operation", "the guidance must tell the customer what to do")
			require.NotNil(t, result.EarliestRetryAfter)
			assert.Equal(t, roleAssignmentPropagationRetryAfter, *result.EarliestRetryAfter)
			assert.Equal(t, detectionsBefore+1, testutil.ToFloat64(roleAssignmentPropagationDetections.WithLabelValues(validationName)))
		})
	}
}
