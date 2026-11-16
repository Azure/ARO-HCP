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
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	desTestIdentityURL = "https://mi.example.com"
	desTestTenantID    = "00000000-0000-0000-0000-000000000099"
	desTestScope       = "https://management.example.com/.default"
	desResourceID      = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/customer-rg/providers/Microsoft.Compute/diskEncryptionSets/des"
	smiResourceIDStr   = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"
	capzResourceIDStr  = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/capz"
	smiToken           = "smi-token"
	capzToken          = "capz-token"
)

func desTestCluster(withCAPZ bool) *coreapi.Cluster {
	controlPlaneOperators := map[string]*azcorearm.ResourceID{}
	if withCAPZ {
		controlPlaneOperators["cluster-api-azure"] = mustParseResourceID(capzResourceIDStr)
	}
	return &coreapi.Cluster{
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						ControlPlaneOperators:  controlPlaneOperators,
						ServiceManagedIdentity: mustParseResourceID(smiResourceIDStr),
					},
				},
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: desTestIdentityURL,
		},
	}
}

func desTestNodePool(encryptionSetID string) *coreapi.NodePool {
	np := &coreapi.NodePool{}
	if len(encryptionSetID) != 0 {
		np.Properties.Platform.OSDisk.EncryptionSetID = mustParseResourceID(encryptionSetID)
	}
	return np
}

func desDecisionResponse(decision checkaccessv2.AccessDecision) *checkaccessv2.AuthorizationDecisionResponse {
	return &checkaccessv2.AuthorizationDecisionResponse{
		Value: []checkaccessv2.AuthorizationDecision{
			{ActionId: diskEncryptionSetReadAction, AccessDecision: decision},
		},
	}
}

func TestAzureNodePoolDiskEncryptionSetPermissionsValidation(t *testing.T) {
	type setupFunc func(ctrl *gomock.Controller) (azureclient.CheckAccessV2ClientBuilder, azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder)

	noClients := func(ctrl *gomock.Controller) (azureclient.CheckAccessV2ClientBuilder, azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder) {
		return azureclient.NewMockCheckAccessV2ClientBuilder(ctrl), azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
	}

	// tokenRetrieverBuilder wires per-identity MI Dataplane tokens: SMI -> smiToken, CAPZ -> capzToken.
	tokenRetrieverBuilder := func(ctrl *gomock.Controller) *azureclient.MockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder {
		builder := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
		for id, tok := range map[string]string{smiResourceIDStr: smiToken, capzResourceIDStr: capzToken} {
			retriever := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetriever(ctrl)
			retriever.EXPECT().GetToken(gomock.Any(), gomock.Any()).Return(azcore.AccessToken{Token: tok}, nil).AnyTimes()
			builder.EXPECT().Build(desTestIdentityURL, mustParseResourceID(id)).Return(retriever, nil).AnyTimes()
		}
		return builder
	}

	// decisionsByToken maps each identity's token to the CheckAccessV2 decision to return. The
	// CreateAuthorizationRequest mock passes the token through as the subject ObjectId so CheckAccess
	// can route the decision per identity.
	setupWithDecisions := func(decisionsByToken map[string]checkaccessv2.AccessDecision) setupFunc {
		return func(ctrl *gomock.Controller) (azureclient.CheckAccessV2ClientBuilder, azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder) {
			mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
			mockCA := azureclient.NewMockCheckAccessV2Client(ctrl)
			mockCABuilder.EXPECT().Build(desTestTenantID).Return(mockCA, nil)
			mockCA.EXPECT().CreateAuthorizationRequest(desResourceID, []string{diskEncryptionSetReadAction}, gomock.Any()).DoAndReturn(
				func(_ string, _ []string, jwt string) (*checkaccessv2.AuthorizationRequest, error) {
					return &checkaccessv2.AuthorizationRequest{Subject: checkaccessv2.SubjectInfo{Attributes: checkaccessv2.SubjectAttributes{ObjectId: jwt}}}, nil
				}).AnyTimes()
			mockCA.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
					return desDecisionResponse(decisionsByToken[req.Subject.Attributes.ObjectId]), nil
				}).AnyTimes()
			return mockCABuilder, tokenRetrieverBuilder(ctrl)
		}
	}

	tests := []struct {
		name            string
		cluster         *coreapi.Cluster
		nodePool        *coreapi.NodePool
		setup           setupFunc
		wantOutcomeType OutcomeType
		msgContains     []string
	}{
		{
			name:            "no encryption set configured, skip",
			cluster:         desTestCluster(true),
			nodePool:        desTestNodePool(""),
			setup:           noClients,
			wantOutcomeType: OutcomeTypeSkipped,
		},
		{
			name:     "both identities have reader",
			cluster:  desTestCluster(true),
			nodePool: desTestNodePool(desResourceID),
			setup: setupWithDecisions(map[string]checkaccessv2.AccessDecision{
				smiToken:  checkaccessv2.Allowed,
				capzToken: checkaccessv2.Allowed,
			}),
			wantOutcomeType: OutcomeTypePassed,
		},
		{
			name:     "service managed identity missing reader",
			cluster:  desTestCluster(true),
			nodePool: desTestNodePool(desResourceID),
			setup: setupWithDecisions(map[string]checkaccessv2.AccessDecision{
				smiToken:  "NotAllowed",
				capzToken: checkaccessv2.Allowed,
			}),
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     []string{smiResourceIDStr, "Reader role", "az role assignment create", desResourceID},
		},
		{
			name:     "both identities missing reader",
			cluster:  desTestCluster(true),
			nodePool: desTestNodePool(desResourceID),
			setup: setupWithDecisions(map[string]checkaccessv2.AccessDecision{
				smiToken:  "NotAllowed",
				capzToken: "NotAllowed",
			}),
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     []string{smiResourceIDStr, capzResourceIDStr},
		},
		{
			name:            "capz identity not found in cluster",
			cluster:         desTestCluster(false),
			nodePool:        desTestNodePool(desResourceID),
			setup:           noClients,
			wantOutcomeType: OutcomeTypeUnknown,
			msgContains:     []string{"Unable to verify OS disk encryption set permissions."},
		},
		{
			name:     "token retrieval fails",
			cluster:  desTestCluster(true),
			nodePool: desTestNodePool(desResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.CheckAccessV2ClientBuilder, azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder) {
				mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
				mockCA := azureclient.NewMockCheckAccessV2Client(ctrl)
				mockCABuilder.EXPECT().Build(desTestTenantID).Return(mockCA, nil)
				retrieverBuilder := azureclient.NewMockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(ctrl)
				retrieverBuilder.EXPECT().Build(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("MI dataplane unavailable")).AnyTimes()
				return mockCABuilder, retrieverBuilder
			},
			wantOutcomeType: OutcomeTypeUnknown,
			msgContains:     []string{"Unable to verify OS disk encryption set permissions."},
		},
		{
			name:     "check access api fails",
			cluster:  desTestCluster(true),
			nodePool: desTestNodePool(desResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.CheckAccessV2ClientBuilder, azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder) {
				mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
				mockCA := azureclient.NewMockCheckAccessV2Client(ctrl)
				mockCABuilder.EXPECT().Build(desTestTenantID).Return(mockCA, nil)
				mockCA.EXPECT().CreateAuthorizationRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(&checkaccessv2.AuthorizationRequest{}, nil).AnyTimes()
				mockCA.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("service unavailable")).AnyTimes()
				return mockCABuilder, tokenRetrieverBuilder(ctrl)
			},
			wantOutcomeType: OutcomeTypeUnknown,
			msgContains:     []string{"Unable to verify OS disk encryption set permissions."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			caBuilder, retrieverBuilder := tt.setup(ctrl)

			v := NewAzureNodePoolDiskEncryptionSetPermissionsValidation(caBuilder, retrieverBuilder, desTestScope)
			result := v.Validate(context.Background(), tt.cluster, testSubscription(), tt.nodePool)

			if result.Outcome.Type != tt.wantOutcomeType {
				t.Fatalf("Validate() outcome = %s, want %s", result.Outcome.Type, tt.wantOutcomeType)
			}
			cond := result.ToCondition(v.Name())
			for _, want := range tt.msgContains {
				if !strings.Contains(cond.Message, want) {
					t.Errorf("condition message %q does not contain %q", cond.Message, want)
				}
			}
		})
	}
}
