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

package validations

import (
	"context"
	"fmt"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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

type fakeSMIClientBuilder struct {
	uaisClient azureclient.UserAssignedIdentitiesClient
	err        error
}

func (f *fakeSMIClientBuilder) BuilderType() azureclient.ServiceManagedIdentityClientBuilderType {
	return azureclient.ServiceManagedIdentityClientBuilderTypeValue
}

func (f *fakeSMIClientBuilder) UserAssignedIdentitiesClient(context.Context, string, *azcorearm.ResourceID, string) (azureclient.UserAssignedIdentitiesClient, error) {
	return f.uaisClient, f.err
}

type fakeCheckAccessV2Client struct {
	resp *checkaccessv2.AuthorizationDecisionResponse
	err  error
}

func (f *fakeCheckAccessV2Client) CheckAccess(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
	return f.resp, f.err
}

func (f *fakeCheckAccessV2Client) CreateAuthorizationRequest(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
	return nil, nil
}

type fakeCheckAccessV2ClientBuilder struct {
	client azureclient.CheckAccessV2Client
	err    error
}

func (f *fakeCheckAccessV2ClientBuilder) Build(_ string) (azureclient.CheckAccessV2Client, error) {
	return f.client, f.err
}

func ptr[T any](v T) *T { return &v }

func mustParseResourceID(s string) *azcorearm.ResourceID {
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func testCluster(containerRegistryMIResourceID, capzResourceID string) *api.HCPOpenShiftCluster {
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID: mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"),
			},
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Platform: api.CustomerPlatformProfile{
				OperatorsAuthentication: api.OperatorsAuthenticationProfile{
					UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]*azcorearm.ResourceID{
							"cluster-api-azure": mustParseResourceID(capzResourceID),
						},
						ServiceManagedIdentity: mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"),
					},
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ManagedIdentitiesDataPlaneIdentityURL: "https://mi.example.com",
		},
	}

	if containerRegistryMIResourceID != "" {
		cluster.CustomerProperties.Platform.ContainerRegistryPullManagedIdentity = mustParseResourceID(containerRegistryMIResourceID)
	}

	return cluster
}

func testSubscription() *arm.Subscription {
	return &arm.Subscription{
		Properties: &arm.SubscriptionProperties{
			TenantId: ptr("00000000-0000-0000-0000-000000000099"),
		},
	}
}

func TestContainerRegistryPullCredentialsPermissionValidation(t *testing.T) {
	capzResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/capz"
	containerRegistryPullMIResourceID := "/subscriptions/00000000-0000-0000-0000-000000000002/resourceGroups/customer-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/acr-pull"
	capzPrincipalID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	uaisClient := &fakeUserAssignedIdentitiesClient{
		getFunc: func(_ context.Context, _, _ string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
			return armmsi.UserAssignedIdentitiesClientGetResponse{
				Identity: armmsi.Identity{
					Properties: &armmsi.UserAssignedIdentityProperties{
						PrincipalID: ptr(capzPrincipalID),
					},
				},
			}, nil
		},
	}

	tests := []struct {
		name        string
		cluster     *api.HCPOpenShiftCluster
		smiBuilder  azureclient.ServiceManagedIdentityClientBuilder
		caBuilder   azureclient.CheckAccessV2ClientBuilder
		wantErr     bool
		errContains string
	}{
		{
			name:    "no containerRegistry configured, skip",
			cluster: testCluster("", capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: uaisClient,
			},
			caBuilder: &fakeCheckAccessV2ClientBuilder{},
			wantErr:   false,
		},
		{
			name:    "permission allowed",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: uaisClient,
			},
			caBuilder: &fakeCheckAccessV2ClientBuilder{
				client: &fakeCheckAccessV2Client{
					resp: &checkaccessv2.AuthorizationDecisionResponse{
						Value: []checkaccessv2.AuthorizationDecision{
							{
								ActionId:       assignAction,
								AccessDecision: checkaccessv2.Allowed,
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "permission denied",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: uaisClient,
			},
			caBuilder: &fakeCheckAccessV2ClientBuilder{
				client: &fakeCheckAccessV2Client{
					resp: &checkaccessv2.AuthorizationDecisionResponse{
						Value: []checkaccessv2.AuthorizationDecision{
							{
								ActionId:       assignAction,
								AccessDecision: "NotAllowed",
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "does not have assign/action permission",
		},
		{
			name:    "permission denied includes az command",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: uaisClient,
			},
			caBuilder: &fakeCheckAccessV2ClientBuilder{
				client: &fakeCheckAccessV2Client{
					resp: &checkaccessv2.AuthorizationDecisionResponse{
						Value: []checkaccessv2.AuthorizationDecision{
							{
								ActionId:       assignAction,
								AccessDecision: "NotAllowed",
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "az role assignment create",
		},
		{
			name:    "SMI client builder fails",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				err: fmt.Errorf("SMI unavailable"),
			},
			caBuilder:   &fakeCheckAccessV2ClientBuilder{},
			wantErr:     true,
			errContains: "failed to get user assigned identities client",
		},
		{
			name:    "ACR pull MI not found",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: &fakeUserAssignedIdentitiesClient{
					getFunc: func(_ context.Context, rg, name string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
						if rg == "customer-rg" && name == "acr-pull" {
							return armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("not found")
						}
						return armmsi.UserAssignedIdentitiesClientGetResponse{
							Identity: armmsi.Identity{
								Properties: &armmsi.UserAssignedIdentityProperties{
									PrincipalID: ptr(capzPrincipalID),
								},
							},
						}, nil
					},
				},
			},
			caBuilder:   &fakeCheckAccessV2ClientBuilder{},
			wantErr:     true,
			errContains: "container registry pull managed identity",
		},
		{
			name:    "CAPZ identity GET fails",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: &fakeUserAssignedIdentitiesClient{
					getFunc: func(_ context.Context, rg, name string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
						if rg == "rg" && name == "capz" {
							return armmsi.UserAssignedIdentitiesClientGetResponse{}, fmt.Errorf("not found")
						}
						return armmsi.UserAssignedIdentitiesClientGetResponse{
							Identity: armmsi.Identity{
								Properties: &armmsi.UserAssignedIdentityProperties{
									PrincipalID: ptr(capzPrincipalID),
								},
							},
						}, nil
					},
				},
			},
			caBuilder:   &fakeCheckAccessV2ClientBuilder{},
			wantErr:     true,
			errContains: "failed to get CAPZ managed identity",
		},
		{
			name:    "CheckAccess API fails",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: uaisClient,
			},
			caBuilder: &fakeCheckAccessV2ClientBuilder{
				client: &fakeCheckAccessV2Client{
					err: fmt.Errorf("service unavailable"),
				},
			},
			wantErr:     true,
			errContains: "failed to check CAPZ assign/action permission",
		},
		{
			name:    "empty response treated as denied",
			cluster: testCluster(containerRegistryPullMIResourceID, capzResourceID),
			smiBuilder: &fakeSMIClientBuilder{
				uaisClient: uaisClient,
			},
			caBuilder: &fakeCheckAccessV2ClientBuilder{
				client: &fakeCheckAccessV2Client{
					resp: &checkaccessv2.AuthorizationDecisionResponse{
						Value: []checkaccessv2.AuthorizationDecision{},
					},
				},
			},
			wantErr:     true,
			errContains: "does not have assign/action permission",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewContainerRegistryPullCredentialsPermissionValidation(tt.smiBuilder, tt.caBuilder)
			err := v.Validate(context.Background(), testSubscription(), tt.cluster)

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.errContains != "" && err != nil {
				if got := err.Error(); !strings.Contains(got, tt.errContains) {
					t.Errorf("error %q does not contain %q", got, tt.errContains)
				}
			}
		})
	}
}
