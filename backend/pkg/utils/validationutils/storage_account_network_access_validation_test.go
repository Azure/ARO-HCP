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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// newTestPolicyAssignmentListPager returns a one-page policy assignment list
// pager for tests. If fetchErr is non-nil, the first page fetch fails with that
// error.
func newTestPolicyAssignmentListPager(assignments []*armpolicy.Assignment, fetchErr error) *runtime.Pager[armpolicy.AssignmentsClientListResponse] {
	pages := []armpolicy.AssignmentsClientListResponse{{
		AssignmentListResult: armpolicy.AssignmentListResult{Value: assignments},
	}}
	idx := -1
	return runtime.NewPager(runtime.PagingHandler[armpolicy.AssignmentsClientListResponse]{
		More: func(page armpolicy.AssignmentsClientListResponse) bool {
			return idx+1 < len(pages)
		},
		Fetcher: func(ctx context.Context, page *armpolicy.AssignmentsClientListResponse) (armpolicy.AssignmentsClientListResponse, error) {
			if fetchErr != nil {
				return armpolicy.AssignmentsClientListResponse{}, fetchErr
			}
			idx++
			return pages[idx], nil
		},
	})
}

func makeTestPolicyAssignment(name string, policyDefinitionID string, enforcementMode *armpolicy.EnforcementMode, parameters map[string]*armpolicy.ParameterValuesValue) *armpolicy.Assignment {
	return &armpolicy.Assignment{
		Name: ptr.To(name),
		Properties: &armpolicy.AssignmentProperties{
			DisplayName:        ptr.To(name),
			PolicyDefinitionID: ptr.To(policyDefinitionID),
			EnforcementMode:    enforcementMode,
			Parameters:         parameters,
		},
	}
}

func TestStorageAccountNetworkAccessValidation_Validate(t *testing.T) {
	const (
		testTenantID       = "11111111-1111-1111-1111-111111111111"
		testSubscriptionID = "00000000-0000-0000-0000-000000000000"
		testManagedRG      = "test-managed-rg"
		customPolicyID     = "/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/policyDefinitions/custom-storage-policy"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))

	clusterSubscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}

	cluster := &coreapi.Cluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: clusterResourceID},
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				ManagedResourceGroup: testManagedRG,
			},
		},
	}

	tests := []struct {
		name                 string
		assignments          []*armpolicy.Assignment
		assignmentsErr       error
		setupDefinitionMock  func(*azureclient.MockPolicyDefinitionsClient)
		wantOutcome          OutcomeType
		wantInternalContains string
	}{
		{
			name:        "no policy assignments returns passed",
			assignments: nil,
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "built-in policy with default effect (Audit) returns passed",
			assignments: []*armpolicy.Assignment{
				makeTestPolicyAssignment("default-effect-public-storage", knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID, nil, nil),
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "built-in policy explicitly overridden to Deny returns failed",
			assignments: []*armpolicy.Assignment{
				makeTestPolicyAssignment("deny-public-storage", knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID, nil,
					map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}}),
			},
			wantOutcome:          OutcomeTypeFailed,
			wantInternalContains: "deny-public-storage",
		},
		{
			name: "built-in policy overridden to Audit returns passed",
			assignments: []*armpolicy.Assignment{
				makeTestPolicyAssignment("audit-public-storage", knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID, nil,
					map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Audit"}}),
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "built-in deny policy excluding the managed resource group via NotScopes returns passed",
			assignments: []*armpolicy.Assignment{
				{
					Name: ptr.To("deny-public-storage-excluded"),
					Properties: &armpolicy.AssignmentProperties{
						DisplayName:        ptr.To("deny-public-storage-excluded"),
						PolicyDefinitionID: ptr.To(knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID),
						Parameters:         map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}},
						NotScopes:          []*string{ptr.To("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRG)},
					},
				},
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "built-in deny policy excluding an unrelated resource group still returns failed",
			assignments: []*armpolicy.Assignment{
				{
					Name: ptr.To("deny-public-storage-unrelated-exclusion"),
					Properties: &armpolicy.AssignmentProperties{
						DisplayName:        ptr.To("deny-public-storage-unrelated-exclusion"),
						PolicyDefinitionID: ptr.To(knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID),
						Parameters:         map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}},
						NotScopes:          []*string{ptr.To("/subscriptions/" + testSubscriptionID + "/resourceGroups/some-other-rg")},
					},
				},
			},
			wantOutcome:          OutcomeTypeFailed,
			wantInternalContains: "deny-public-storage-unrelated-exclusion",
		},
		{
			name: "built-in deny policy excluding the ancestor subscription via NotScopes returns passed",
			assignments: []*armpolicy.Assignment{
				{
					Name: ptr.To("deny-public-storage-sub-excluded"),
					Properties: &armpolicy.AssignmentProperties{
						DisplayName:        ptr.To("deny-public-storage-sub-excluded"),
						PolicyDefinitionID: ptr.To(knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID),
						Parameters:         map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}},
						NotScopes:          []*string{ptr.To("/subscriptions/" + testSubscriptionID)},
					},
				},
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "built-in deny policy with resource selectors returns passed (applicability cannot be confirmed)",
			assignments: []*armpolicy.Assignment{
				{
					Name: ptr.To("deny-public-storage-with-selectors"),
					Properties: &armpolicy.AssignmentProperties{
						DisplayName:        ptr.To("deny-public-storage-with-selectors"),
						PolicyDefinitionID: ptr.To(knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID),
						Parameters:         map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}},
						ResourceSelectors:  []*armpolicy.ResourceSelector{{Name: ptr.To("selector1")}},
					},
				},
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "built-in deny policy with DoNotEnforce enforcement mode returns passed",
			assignments: []*armpolicy.Assignment{
				makeTestPolicyAssignment("deny-public-storage-not-enforced", knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID,
					ptr.To(armpolicy.EnforcementModeDoNotEnforce), nil),
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name: "custom policy with hardcoded deny effect returns failed",
			assignments: []*armpolicy.Assignment{
				makeTestPolicyAssignment("custom-deny-public-storage", customPolicyID, nil, nil),
			},
			setupDefinitionMock: func(m *azureclient.MockPolicyDefinitionsClient) {
				m.EXPECT().Get(gomock.Any(), "custom-storage-policy", nil).Return(armpolicy.DefinitionsClientGetResponse{
					Definition: armpolicy.Definition{
						Properties: &armpolicy.DefinitionProperties{
							PolicyRule: map[string]any{
								"if": map[string]any{
									"allOf": []any{
										map[string]any{"field": "type", "equals": "Microsoft.Storage/storageAccounts"},
										map[string]any{"field": "Microsoft.Storage/storageAccounts/publicNetworkAccess", "equals": "Enabled"},
									},
								},
								"then": map[string]any{"effect": "deny"},
							},
						},
					},
				}, nil)
			},
			wantOutcome:          OutcomeTypeFailed,
			wantInternalContains: "custom-deny-public-storage",
		},
		{
			name: "custom policy unrelated to storage accounts returns passed",
			assignments: []*armpolicy.Assignment{
				makeTestPolicyAssignment("custom-unrelated", customPolicyID, nil, nil),
			},
			setupDefinitionMock: func(m *azureclient.MockPolicyDefinitionsClient) {
				m.EXPECT().Get(gomock.Any(), "custom-storage-policy", nil).Return(armpolicy.DefinitionsClientGetResponse{
					Definition: armpolicy.Definition{
						Properties: &armpolicy.DefinitionProperties{
							PolicyRule: map[string]any{
								"if":   map[string]any{"field": "type", "equals": "Microsoft.Compute/virtualMachines"},
								"then": map[string]any{"effect": "deny"},
							},
						},
					},
				}, nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:           "list policy assignments error returns unknown",
			assignmentsErr: errors.New("transient Azure error"),
			wantOutcome:    OutcomeTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			assignmentsClient := azureclient.NewMockPolicyAssignmentsClient(ctrl)
			assignmentsClient.EXPECT().
				NewListPager(&armpolicy.AssignmentsClientListOptions{Filter: to.Ptr("atScope()")}).
				Return(newTestPolicyAssignmentListPager(tt.assignments, tt.assignmentsErr))

			definitionsClient := azureclient.NewMockPolicyDefinitionsClient(ctrl)
			if tt.setupDefinitionMock != nil {
				tt.setupDefinitionMock(definitionsClient)
			}

			fpaBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			fpaBuilder.EXPECT().PolicyAssignmentsClient(testTenantID, testSubscriptionID).Return(assignmentsClient, nil)
			if tt.assignmentsErr == nil {
				fpaBuilder.EXPECT().PolicyDefinitionsClient(testTenantID, testSubscriptionID).Return(definitionsClient, nil)
			}

			v := NewStorageAccountNetworkAccessValidation(fpaBuilder)
			result := v.Validate(context.Background(), clusterSubscription, cluster)

			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalContains != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalContains)
			}
		})
	}
}

func TestStorageAccountNetworkAccessValidation_Name(t *testing.T) {
	v := NewStorageAccountNetworkAccessValidation(nil)
	assert.Equal(t, "StorageAccountNetworkAccessValidation", v.Name())
}
