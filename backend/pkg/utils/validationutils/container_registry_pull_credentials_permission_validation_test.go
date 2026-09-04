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

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

type fakeUserAssignedIdentitiesClient struct {
	getFunc func(ctx context.Context, rg, name string, opts *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error)
}

func (f *fakeUserAssignedIdentitiesClient) Get(ctx context.Context, rg, name string, opts *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
	return f.getFunc(ctx, rg, name, opts)
}

func (f *fakeUserAssignedIdentitiesClient) CreateOrUpdate(context.Context, string, string, armmsi.Identity, *armmsi.UserAssignedIdentitiesClientCreateOrUpdateOptions) (armmsi.UserAssignedIdentitiesClientCreateOrUpdateResponse, error) {
	return armmsi.UserAssignedIdentitiesClientCreateOrUpdateResponse{}, nil
}

func (f *fakeUserAssignedIdentitiesClient) Delete(context.Context, string, string, *armmsi.UserAssignedIdentitiesClientDeleteOptions) (armmsi.UserAssignedIdentitiesClientDeleteResponse, error) {
	return armmsi.UserAssignedIdentitiesClientDeleteResponse{}, nil
}

func mustParseResourceID(s string) *azcorearm.ResourceID {
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func containerRegistryTestCluster(containerRegistryMIResourceID, capzResourceID string) *coreapi.HCPOpenShiftCluster {
	cluster := &coreapi.HCPOpenShiftCluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID: mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"),
			},
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Platform: coreapi.CustomerPlatformProfile{
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]*azcorearm.ResourceID{
							"cluster-api-azure": mustParseResourceID(capzResourceID),
						},
						ServiceManagedIdentity: mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"),
					},
				},
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: "https://mi.example.com",
		},
	}

	if containerRegistryMIResourceID != "" {
		cluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity = mustParseResourceID(containerRegistryMIResourceID)
	}

	return cluster
}

func testSubscription() *coreapi.Subscription {
	return &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To("00000000-0000-0000-0000-000000000099"),
		},
	}
}

func TestContainerRegistryPullCredentialsPermissionValidation(t *testing.T) {
	capzMIResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/capz"
	// Same-subscription pull MI — the only supported case; cross-subscription is rejected at validation.
	containerRegistryPullMIResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/customer-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/acr-pull"
	crossSubContainerRegistryPullMIResourceID := "/subscriptions/00000000-0000-0000-0000-000000000002/resourceGroups/customer-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/acr-pull"

	acrPullMISubscription := "00000000-0000-0000-0000-000000000001"
	capzMISubscription := "00000000-0000-0000-0000-000000000001"
	testTenantID := "00000000-0000-0000-0000-000000000099"
	testIdentityURL := "https://mi.example.com"
	smiResourceID := mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi")
	capzPrincipalID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	successfulGetResponse := armmsi.UserAssignedIdentitiesClientGetResponse{
		Identity: armmsi.Identity{
			Properties: &armmsi.UserAssignedIdentityProperties{
				PrincipalID: ptr.To(capzPrincipalID),
			},
		},
	}

	uaisClient := &fakeUserAssignedIdentitiesClient{
		getFunc: func(_ context.Context, _, _ string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
			return successfulGetResponse, nil
		},
	}

	type setupFunc func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder)

	setupBothClients := func(caResp *checkaccessv2.AuthorizationDecisionResponse, caErr error) setupFunc {
		return func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder) {
			mockSMI := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			mockSMI.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, acrPullMISubscription).Return(uaisClient, nil)
			mockSMI.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, capzMISubscription).Return(uaisClient, nil)

			mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
			mockCA := azureclient.NewMockCheckAccessV2Client(ctrl)
			mockCABuilder.EXPECT().Build(testTenantID).Return(mockCA, nil)
			mockCA.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Return(caResp, caErr)

			return mockSMI, mockCABuilder
		}
	}

	tests := []struct {
		name            string
		cluster         *coreapi.HCPOpenShiftCluster
		setup           setupFunc
		wantOutcomeType OutcomeType
		msgContains     string
	}{
		{
			name:    "cross-subscription pull MI rejected",
			cluster: containerRegistryTestCluster(crossSubContainerRegistryPullMIResourceID, capzMIResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder) {
				return azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl), azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
			},
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     "same subscription",
		},
		{
			name:    "no containerRegistry configured, skip",
			cluster: containerRegistryTestCluster("", capzMIResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder) {
				mockSMI := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
				mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
				return mockSMI, mockCABuilder
			},
			wantOutcomeType: OutcomeTypeSkipped,
		},
		{
			name:    "permission allowed",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: setupBothClients(&checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: assignAction, AccessDecision: checkaccessv2.Allowed},
				},
			}, nil),
			wantOutcomeType: OutcomeTypePassed,
		},
		{
			name:    "permission denied",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: setupBothClients(&checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: assignAction, AccessDecision: "NotAllowed"},
				},
			}, nil),
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     "does not have assign/action permission",
		},
		{
			name:    "permission denied includes az command",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: setupBothClients(&checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: assignAction, AccessDecision: "NotAllowed"},
				},
			}, nil),
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     "az role assignment create",
		},
		{
			name:    "SMI client builder fails",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder) {
				mockSMI := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
				mockSMI.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, acrPullMISubscription).Return(nil, fmt.Errorf("SMI unavailable"))
				mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
				return mockSMI, mockCABuilder
			},
			wantOutcomeType: OutcomeTypeUnknown,
			msgContains:     "Unable to verify container registry pull credentials permissions.",
		},
		{
			name:    "ACR pull MI not found",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder) {
				notFoundClient := &fakeUserAssignedIdentitiesClient{
					getFunc: func(_ context.Context, rg, name string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
						if rg == "customer-rg" && name == "acr-pull" {
							return armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("not found")
						}
						return successfulGetResponse, nil
					},
				}
				mockSMI := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
				mockSMI.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, acrPullMISubscription).Return(notFoundClient, nil)
				mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
				return mockSMI, mockCABuilder
			},
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     "Container registry pull managed identity",
		},
		{
			name:    "CAPZ identity GET fails",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: func(ctrl *gomock.Controller) (azureclient.ServiceManagedIdentityClientBuilder, azureclient.CheckAccessV2ClientBuilder) {
				capzFailClient := &fakeUserAssignedIdentitiesClient{
					getFunc: func(_ context.Context, rg, name string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
						if rg == "rg" && name == "capz" {
							return armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("not found")
						}
						return successfulGetResponse, nil
					},
				}
				mockSMI := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
				mockSMI.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, acrPullMISubscription).Return(capzFailClient, nil)
				mockSMI.EXPECT().UserAssignedIdentitiesClient(gomock.Any(), testIdentityURL, smiResourceID, capzMISubscription).Return(capzFailClient, nil)
				mockCABuilder := azureclient.NewMockCheckAccessV2ClientBuilder(ctrl)
				return mockSMI, mockCABuilder
			},
			wantOutcomeType: OutcomeTypeUnknown,
			msgContains:     "Unable to verify container registry pull credentials permissions.",
		},
		{
			name:            "CheckAccess API fails",
			cluster:         containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup:           setupBothClients(nil, fmt.Errorf("service unavailable")),
			wantOutcomeType: OutcomeTypeUnknown,
			msgContains:     "Unable to verify container registry pull credentials permissions.",
		},
		{
			name:    "empty response treated as denied",
			cluster: containerRegistryTestCluster(containerRegistryPullMIResourceID, capzMIResourceID),
			setup: setupBothClients(&checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{},
			}, nil),
			wantOutcomeType: OutcomeTypeFailed,
			msgContains:     "does not have assign/action permission",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			smiBuilder, caBuilder := tt.setup(ctrl)

			v := NewContainerRegistryPullCredentialsPermissionValidation(smiBuilder, caBuilder)
			result := v.Validate(context.Background(), testSubscription(), tt.cluster)

			if result.Outcome.Type != tt.wantOutcomeType {
				t.Fatalf("Validate() outcome = %s, want %s", result.Outcome.Type, tt.wantOutcomeType)
			}
			if tt.msgContains != "" {
				cond := result.ToCondition(v.Name())
				if !strings.Contains(cond.Message, tt.msgContains) {
					t.Errorf("condition message %q does not contain %q", cond.Message, tt.msgContains)
				}
			}
		})
	}
}

func TestContainerRegistryPullCredentialsPermissionValidation_InputKey(t *testing.T) {
	capzResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/capz"
	miA := "/subscriptions/00000000-0000-0000-0000-000000000002/resourceGroups/customer-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/mi-a"
	miB := "/subscriptions/00000000-0000-0000-0000-000000000002/resourceGroups/customer-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/mi-b"

	v := NewContainerRegistryPullCredentialsPermissionValidation(nil, nil)

	t.Run("nil MI returns empty string", func(t *testing.T) {
		cluster := containerRegistryTestCluster("", capzResourceID)
		if got := v.InputKey(cluster); got != "" {
			t.Errorf("InputKey() = %q, want empty string", got)
		}
	})

	t.Run("returns MI resource ID string", func(t *testing.T) {
		cluster := containerRegistryTestCluster(miA, capzResourceID)
		got := v.InputKey(cluster)
		if !strings.EqualFold(got, miA) {
			t.Errorf("InputKey() = %q, want %q", got, miA)
		}
	})

	t.Run("different MIs return different keys", func(t *testing.T) {
		clusterA := containerRegistryTestCluster(miA, capzResourceID)
		clusterB := containerRegistryTestCluster(miB, capzResourceID)
		keyA := v.InputKey(clusterA)
		keyB := v.InputKey(clusterB)
		if keyA == keyB {
			t.Errorf("InputKey() returned same key for different MIs: %q", keyA)
		}
	})
}
