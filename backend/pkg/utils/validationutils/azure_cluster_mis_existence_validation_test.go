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
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestAzureClusterManagedIdentitiesExistenceValidation_Validate(t *testing.T) {
	const (
		testSubscriptionID = "00000000-0000-0000-0000-000000000000"
		testIdentityURL    = "https://identity.example.com"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	cpOperatorResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-cp-operator"))
	dpOperatorResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-dp-operator"))

	newCluster := func() *coreapi.Cluster {
		return &coreapi.Cluster{
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: clusterResourceID},
			},
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
				ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
			},
			CustomerProperties: coreapi.ClusterCustomerProperties{
				Platform: coreapi.CustomerPlatformProfile{
					OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
						UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
							ControlPlaneOperators: map[string]*azcorearm.ResourceID{
								"test-cp-operator": cpOperatorResourceID,
							},
							DataPlaneOperators: map[string]*azcorearm.ResourceID{
								"test-dp-operator": dpOperatorResourceID,
							},
							ServiceManagedIdentity: smiResourceID,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name                string
		setupMocks          func(*azureclient.MockServiceManagedIdentityClientBuilder, *azureclient.MockUserAssignedIdentitiesClient)
		wantOutcome         OutcomeType
		wantReason          string
		wantUserMessageHas  string
		wantInternalMessage string
	}{
		{
			name: "SMI client builder returns identity not found error",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, _ *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(nil, azureclient.ErrSMIClientBuilderIdentityNotFound)
			},
			wantOutcome:        OutcomeTypeFailed,
			wantReason:         "ManagedIdentitiesNotFound",
			wantUserMessageHas: smiResourceID.String(),
		},
		{
			name: "SMI client builder returns generic error",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, _ *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(nil, fmt.Errorf("transient failure"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantReason:          "InternalError",
			wantInternalMessage: "transient failure",
		},
		{
			name: "multiple operator identities not found",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, uaisClient *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(uaisClient, nil)
				uaisClient.EXPECT().
					Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, &azcore.ResponseError{ErrorCode: "ResourceNotFound"}).
					Times(2)
			},
			wantOutcome: OutcomeTypeFailed,
			wantReason:  "ManagedIdentitiesNotFound",
			// Both identities are missing: the loop must not stop after the first NotFound (it
			// should `continue`, not `return`), and both must end up in the joined message.
			wantInternalMessage: "Managed identities not found: " + cpOperatorResourceID.String() + ", " + dpOperatorResourceID.String(),
		},
		{
			name: "operator identity not found because of Azure returning an azcore.Error with HTTP 404 status code with a different code than ResourceNotFound",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, uaisClient *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(uaisClient, nil)
				uaisClient.EXPECT().
					Get(gomock.Any(), cpOperatorResourceID.ResourceGroupName, cpOperatorResourceID.Name, nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "AnotherErrorCode"})
				uaisClient.EXPECT().
					Get(gomock.Any(), dpOperatorResourceID.ResourceGroupName, dpOperatorResourceID.Name, nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantReason:          "ManagedIdentitiesNotFound",
			wantInternalMessage: "Managed identities not found: " + cpOperatorResourceID.String(),
		},
		{
			name: "one operator identity not found, the other exists",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, uaisClient *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(uaisClient, nil)
				uaisClient.EXPECT().
					Get(gomock.Any(), cpOperatorResourceID.ResourceGroupName, cpOperatorResourceID.Name, nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, &azcore.ResponseError{ErrorCode: "ResourceNotFound"})
				uaisClient.EXPECT().
					Get(gomock.Any(), dpOperatorResourceID.ResourceGroupName, dpOperatorResourceID.Name, nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantReason:          "ManagedIdentitiesNotFound",
			wantInternalMessage: "Managed identities not found: " + cpOperatorResourceID.String(),
		},
		{
			name: "operator identity Get returns generic error stops checking remaining identities",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, uaisClient *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(uaisClient, nil)
				// Control plane operator is checked first; a generic error here must abort
				// immediately, so the data plane operator's Get must never be called.
				uaisClient.EXPECT().
					Get(gomock.Any(), cpOperatorResourceID.ResourceGroupName, cpOperatorResourceID.Name, nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("internal server error")).
					Times(1)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantReason:          "InternalError",
			wantInternalMessage: "internal server error",
		},
		{
			name: "all managed identities exist",
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, uaisClient *azureclient.MockUserAssignedIdentitiesClient) {
				builder.EXPECT().
					UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).
					Return(uaisClient, nil)
				uaisClient.EXPECT().
					Get(gomock.Any(), gomock.Any(), gomock.Any(), nil).
					Return(armmsi.UserAssignedIdentitiesClientGetResponse{}, nil).
					Times(2)
			},
			wantOutcome: OutcomeTypePassed,
			wantReason:  string(coreapi.ControllerConditionReasonAsExpected),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			mockUAISClient := azureclient.NewMockUserAssignedIdentitiesClient(ctrl)
			tt.setupMocks(mockBuilder, mockUAISClient)

			v := NewAzureClusterManagedIdentitiesExistenceValidation(mockBuilder)
			result := v.Validate(context.Background(), &coreapi.Subscription{}, newCluster())

			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			assert.Equal(t, tt.wantReason, result.Reason())
			if tt.wantUserMessageHas != "" {
				switch tt.wantOutcome {
				case OutcomeTypeFailed:
					assert.Contains(t, result.Outcome.Failed.UserMessage, tt.wantUserMessageHas)
				case OutcomeTypeUnknown:
					assert.Contains(t, result.Outcome.Unknown.UserMessage, tt.wantUserMessageHas)
				}
			}
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}

func TestAzureClusterManagedIdentitiesExistenceValidation_clusterOperatorsManagedIdentities(t *testing.T) {
	const testSubscriptionID = "00000000-0000-0000-0000-000000000000"

	cpOperatorResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-cp-operator"))
	dpOperatorResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-dp-operator"))

	tests := []struct {
		name    string
		profile *coreapi.UserAssignedIdentitiesProfile
		want    []*azcorearm.ResourceID
	}{
		{
			name: "control and data plane operators are combined",
			profile: &coreapi.UserAssignedIdentitiesProfile{
				ControlPlaneOperators: map[string]*azcorearm.ResourceID{
					"test-cp-operator": cpOperatorResourceID,
				},
				DataPlaneOperators: map[string]*azcorearm.ResourceID{
					"test-dp-operator": dpOperatorResourceID,
				},
			},
			want: []*azcorearm.ResourceID{cpOperatorResourceID, dpOperatorResourceID},
		},
		{
			name:    "no operators returns nil",
			profile: &coreapi.UserAssignedIdentitiesProfile{},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &AzureClusterManagedIdentitiesExistenceValidation{}
			result := v.clusterOperatorsManagedIdentities(tt.profile)
			assert.ElementsMatch(t, tt.want, result)
		})
	}
}
