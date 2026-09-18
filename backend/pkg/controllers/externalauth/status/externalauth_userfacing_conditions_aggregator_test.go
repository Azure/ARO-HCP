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

func TestIsUserFacingCondition(t *testing.T) {
	tests := []struct {
		condType string
		want     bool
	}{
		{"ConsoleAvailable", true},
		{"CliAvailable", true},
		{"Available", true},
		{"FooBarAvailable", true},
		{"SomeOtherCondition", false},
		{"Degraded", false},
		{"InternalOnly", false},
		{"Progressing", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.condType), func(t *testing.T) {
			assert.Equal(t, tc.want, isUserFacingCondition(tc.condType))
		})
	}
}

func TestExternalAuthUserFacingConditionsAggregator_SyncOnce(t *testing.T) {
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	consoleAvailableTrue := metav1.Condition{
		Type:    coreapi.PerClientAvailableConditionType("console"),
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
		Message: "OIDC config is available",
	}
	cliAvailableTrue := metav1.Condition{
		Type:    coreapi.PerClientAvailableConditionType("cli"),
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
		Message: "Public client does not require a secret",
	}
	consoleAvailableFalse := metav1.Condition{
		Type:    coreapi.PerClientAvailableConditionType("console"),
		Status:  metav1.ConditionFalse,
		Reason:  coreapi.ExternalAuthConfidentialReasonAwaitingSecret,
		Message: "Waiting for secret",
	}
	nonUserFacingCondition := metav1.Condition{
		Type:    "InternalOnly",
		Status:  metav1.ConditionTrue,
		Reason:  "Internal",
		Message: "should not be promoted",
	}

	tests := []struct {
		name string

		externalAuth                *coreapi.HCPOpenShiftClusterExternalAuth
		serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth

		expectNoWrite  bool
		wantConditions []metav1.Condition
	}{
		{
			name:         "lifts ConsoleAvailable condition from SPEA to ExternalAuth",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableTrue}
			}),
			wantConditions: []metav1.Condition{consoleAvailableTrue},
		},
		{
			name:         "lifts AwaitingSecret condition from SPEA to ExternalAuth",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableFalse}
			}),
			wantConditions: []metav1.Condition{consoleAvailableFalse},
		},
		{
			name:         "lifts multiple per-client Available conditions",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableTrue, cliAvailableTrue}
			}),
			wantConditions: []metav1.Condition{consoleAvailableTrue, cliAvailableTrue},
		},
		{
			name:         "does not promote non-Available conditions",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{
					consoleAvailableTrue,
					nonUserFacingCondition,
				}
			}),
			wantConditions: []metav1.Condition{consoleAvailableTrue},
		},
		{
			name: "does not promote Degraded or Progressing conditions",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{
					consoleAvailableTrue,
					{Type: "Degraded", Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "no errors"},
					{Type: "Progressing", Status: metav1.ConditionFalse, Reason: "Idle"},
				}
			}),
			wantConditions: []metav1.Condition{consoleAvailableTrue},
		},
		{
			name: "removes stale conditions no longer on SPEA",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{
					consoleAvailableTrue,
					cliAvailableTrue,
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableTrue}
			}),
			wantConditions: []metav1.Condition{consoleAvailableTrue},
		},
		{
			name: "removes all Available conditions when SPEA has none",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{
					consoleAvailableTrue,
					cliAvailableTrue,
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			wantConditions:              nil,
		},
		{
			name: "preserves non-Available conditions already on ExternalAuth",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{
					{Type: "SomeOtherType", Status: metav1.ConditionTrue, Reason: "External", Message: "set by another controller"},
					consoleAvailableTrue,
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableFalse}
			}),
			wantConditions: []metav1.Condition{
				{Type: "SomeOtherType", Status: metav1.ConditionTrue, Reason: "External", Message: "set by another controller"},
				consoleAvailableFalse,
			},
		},
		{
			name: "updates existing condition status from True to False",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{consoleAvailableTrue}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableFalse}
			}),
			wantConditions: []metav1.Condition{consoleAvailableFalse},
		},
		{
			name:                        "no-op when SPEA not found",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: nil,
			expectNoWrite:               true,
		},
		{
			name: "no-op when UserFacingConditions already match SPEA",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{consoleAvailableTrue}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{consoleAvailableTrue}
			}),
			expectNoWrite: true,
		},
		{
			name:                        "no-op when SPEA has no conditions and ExternalAuth has no UserFacingConditions",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectNoWrite:               true,
		},
		{
			name: "no-op when SPEA only has non-Available conditions and ExternalAuth has none",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{nonUserFacingCondition}
			}),
			expectNoWrite: true,
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

			if tc.wantConditions == nil {
				assert.Empty(t, updatedEA.Status.UserFacingConditions,
					"expected no user-facing conditions")
				return
			}

			require.Len(t, updatedEA.Status.UserFacingConditions, len(tc.wantConditions),
				"expected %d user-facing conditions", len(tc.wantConditions))
			for _, want := range tc.wantConditions {
				cond := apimeta.FindStatusCondition(updatedEA.Status.UserFacingConditions, want.Type)
				require.NotNil(t, cond, fmt.Sprintf("aggregator must set the %s condition", want.Type))
				assert.Equal(t, want.Status, cond.Status, "status for %s", want.Type)
				assert.Equal(t, want.Reason, cond.Reason, "reason for %s", want.Type)
				assert.Equal(t, want.Message, cond.Message, "message for %s", want.Type)
			}
		})
	}
}
