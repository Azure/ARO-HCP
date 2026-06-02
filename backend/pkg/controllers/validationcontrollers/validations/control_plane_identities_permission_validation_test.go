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
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// --- Mocks ---

type mockSMIClientBuilder struct {
	identityCredentialFn func(ctx context.Context, clusterIdentityURL string, identityResourceID *azcorearm.ResourceID) (azcore.TokenCredential, error)
	subnetsClientFn      func(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (azureclient.SubnetsClient, error)
}

func (m *mockSMIClientBuilder) BuilderType() azureclient.ServiceManagedIdentityClientBuilderType {
	return azureclient.ServiceManagedIdentityClientBuilderTypeValue
}

func (m *mockSMIClientBuilder) UserAssignedIdentitiesClient(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.UserAssignedIdentitiesClient, error) {
	return nil, nil
}

func (m *mockSMIClientBuilder) SubnetsClient(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (azureclient.SubnetsClient, error) {
	return m.subnetsClientFn(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
}

func (m *mockSMIClientBuilder) IdentityCredential(ctx context.Context, clusterIdentityURL string, identityResourceID *azcorearm.ResourceID) (azcore.TokenCredential, error) {
	return m.identityCredentialFn(ctx, clusterIdentityURL, identityResourceID)
}

type mockSubnetsClient struct {
	getFn func(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error)
}

func (m *mockSubnetsClient) Get(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
	return m.getFn(ctx, resourceGroupName, virtualNetworkName, subnetName, options)
}

type mockCheckAccessV2ClientBuilder struct {
	buildFn func(tenantID string) (azureclient.CheckAccessV2Client, error)
}

func (m *mockCheckAccessV2ClientBuilder) Build(tenantID string) (azureclient.CheckAccessV2Client, error) {
	return m.buildFn(tenantID)
}

type mockCheckAccessV2Client struct {
	checkAccessFn              func(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error)
	createAuthorizationRequest func(resourceId string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error)
}

func (m *mockCheckAccessV2Client) CheckAccess(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
	return m.checkAccessFn(ctx, authzReq)
}

func (m *mockCheckAccessV2Client) CreateAuthorizationRequest(resourceId string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error) {
	return m.createAuthorizationRequest(resourceId, actions, jwtToken)
}

type mockTokenCredential struct {
	getTokenFn func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error)
}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return m.getTokenFn(ctx, options)
}

type mockRoleDefinitionsCachedReader struct {
	getCachedByIDFn func(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error)
}

func (m *mockRoleDefinitionsCachedReader) GetCachedByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
	return m.getCachedByIDFn(ctx, roleDefinitionResourceID, options)
}

// --- Test helpers ---

func mustParseResourceID(id string) *azcorearm.ResourceID {
	rid, err := azcorearm.ParseResourceID(id)
	if err != nil {
		panic(err)
	}
	return rid
}

func newTestCluster() *api.HCPOpenShiftCluster {
	cluster := api.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Platform.OperatorsAuthentication = api.OperatorsAuthenticationProfile{
		UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
			ControlPlaneOperators: map[string]*azcorearm.ResourceID{
				"CloudController": mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-controller"),
			},
			ServiceManagedIdentity: mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"),
		},
	}
	return cluster
}

func newTestSubscription() *arm.Subscription {
	sub := api.CreateTestSubscription()
	sub.Properties.TenantId = ptr.To(api.TestTenantID)
	return sub
}

func newRoleDefinitionResponse(actions ...string) armauthorization.RoleDefinitionsClientGetByIDResponse {
	permissions := make([]*armauthorization.Permission, 1)
	actionsSlice := make([]*string, len(actions))
	for i := range actions {
		actionsSlice[i] = ptr.To(actions[i])
	}
	permissions[0] = &armauthorization.Permission{Actions: actionsSlice}
	return armauthorization.RoleDefinitionsClientGetByIDResponse{
		RoleDefinition: armauthorization.RoleDefinition{
			ID: ptr.To("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def"),
			Properties: &armauthorization.RoleDefinitionProperties{
				Permissions: permissions,
			},
		},
	}
}

// --- Tests ---

func TestValidate_AllPermissionsAllowed(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	identitiesConfig := &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			"CloudController": &azure.ControlPlaneOperatorIdentity{
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{ResourceID: roleDefResourceID},
						},
					},
				},
			},
		},
	}

	mockCheckAccessClient := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(resourceId string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return &mockSubnetsClient{
					getFn: func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
						return armnetwork.SubnetsClientGetResponse{
							Subnet: armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}},
						}, nil
					},
				}, nil
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return &mockTokenCredential{
					getTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
					},
				}, nil
			},
		},
		identitiesConfig,
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(
						"Microsoft.Network/networkSecurityGroups/read",
						"Microsoft.Network/networkSecurityGroups/write",
						"Microsoft.Network/networkSecurityGroups/join/action",
						"Microsoft.Network/virtualNetworks/join/action",
						"Microsoft.Network/virtualNetworks/read",
						"Microsoft.Network/virtualNetworks/write",
						"Microsoft.Network/virtualNetworks/subnets/join/action",
						"Microsoft.Network/virtualNetworks/subnets/read",
						"Microsoft.Network/virtualNetworks/subnets/write",
					), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return mockCheckAccessClient, nil
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	assert.NoError(t, err)
}

func TestValidate_MissingPermissions(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	identitiesConfig := &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			"CloudController": &azure.ControlPlaneOperatorIdentity{
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{ResourceID: roleDefResourceID},
						},
					},
				},
			},
		},
	}

	mockCheckAccessClient := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(resourceId string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied},
				},
			}, nil
		},
	}

	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return &mockSubnetsClient{
					getFn: func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
						return armnetwork.SubnetsClientGetResponse{
							Subnet: armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}},
						}, nil
					},
				}, nil
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return &mockTokenCredential{
					getTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
					},
				}, nil
			},
		},
		identitiesConfig,
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(
						"Microsoft.Network/networkSecurityGroups/read",
						"Microsoft.Network/networkSecurityGroups/write",
						"Microsoft.Network/networkSecurityGroups/join/action",
						"Microsoft.Network/virtualNetworks/join/action",
						"Microsoft.Network/virtualNetworks/read",
						"Microsoft.Network/virtualNetworks/write",
						"Microsoft.Network/virtualNetworks/subnets/join/action",
						"Microsoft.Network/virtualNetworks/subnets/read",
						"Microsoft.Network/virtualNetworks/subnets/write",
					), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return mockCheckAccessClient, nil
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "control plane operators missing required permissions")
	assert.Contains(t, err.Error(), "not allowed")
	assert.Contains(t, err.Error(), "denied")
}

func TestValidate_IdentityCredentialError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	identitiesConfig := &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			"CloudController": &azure.ControlPlaneOperatorIdentity{
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{ResourceID: roleDefResourceID},
						},
					},
				},
			},
		},
	}

	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return &mockSubnetsClient{
					getFn: func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
						return armnetwork.SubnetsClientGetResponse{
							Subnet: armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}},
						}, nil
					},
				}, nil
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return nil, errors.New("failed to connect to MI dataplane")
			},
		},
		identitiesConfig,
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(
						"Microsoft.Network/networkSecurityGroups/read",
					), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return &mockCheckAccessV2Client{}, nil
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get identity credential")
}

func TestValidate_GetTokenError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	identitiesConfig := &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			"CloudController": &azure.ControlPlaneOperatorIdentity{
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{ResourceID: roleDefResourceID},
						},
					},
				},
			},
		},
	}

	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return &mockSubnetsClient{
					getFn: func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
						return armnetwork.SubnetsClientGetResponse{
							Subnet: armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}},
						}, nil
					},
				}, nil
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return &mockTokenCredential{
					getTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{}, errors.New("token endpoint unavailable")
					},
				}, nil
			},
		},
		identitiesConfig,
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(
						"Microsoft.Network/networkSecurityGroups/read",
					), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return &mockCheckAccessV2Client{}, nil
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token endpoint unavailable")
}

func TestValidate_SubnetClientError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return nil, errors.New("subnet client creation failed")
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return nil, nil
			},
		},
		&azure.ClusterScopedIdentitiesConfig{},
		&cachedreader.BackendIdentityAzureCachedReaders{},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return &mockCheckAccessV2Client{}, nil
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get subnet client")
}

func TestValidate_CheckAccessClientBuildError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return nil, nil
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return nil, nil
			},
		},
		&azure.ClusterScopedIdentitiesConfig{},
		&cachedreader.BackendIdentityAzureCachedReaders{},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return nil, errors.New("check access client build failed")
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to build check access client")
}

func TestValidate_RouteTablePermissionsChecked(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	cluster := newTestCluster()
	subscription := newTestSubscription()

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	identitiesConfig := &azure.ClusterScopedIdentitiesConfig{
		ControlPlaneOperatorsIdentities: azure.ControlPlaneOperatorsIdentities{
			"CloudController": &azure.ControlPlaneOperatorIdentity{
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{
							{ResourceID: roleDefResourceID},
						},
					},
				},
			},
		},
	}

	checkAccessCallCount := 0
	mockCheckAccessClient := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(resourceId string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			checkAccessCallCount++
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	routeTableID := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.Network/routeTables/testRouteTable"
	validation := NewControlPlaneIdentitiesPermissionValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return &mockSubnetsClient{
					getFn: func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
						return armnetwork.SubnetsClientGetResponse{
							Subnet: armnetwork.Subnet{
								Properties: &armnetwork.SubnetPropertiesFormat{
									RouteTable: &armnetwork.RouteTable{
										ID: ptr.To(routeTableID),
									},
								},
							},
						}, nil
					},
				}, nil
			},
			identityCredentialFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID) (azcore.TokenCredential, error) {
				return &mockTokenCredential{
					getTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
					},
				}, nil
			},
		},
		identitiesConfig,
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(
						"Microsoft.Network/networkSecurityGroups/read",
						"Microsoft.Network/networkSecurityGroups/write",
						"Microsoft.Network/networkSecurityGroups/join/action",
						"Microsoft.Network/virtualNetworks/join/action",
						"Microsoft.Network/virtualNetworks/read",
						"Microsoft.Network/virtualNetworks/write",
						"Microsoft.Network/virtualNetworks/subnets/join/action",
						"Microsoft.Network/virtualNetworks/subnets/read",
						"Microsoft.Network/virtualNetworks/subnets/write",
						"Microsoft.Network/routeTables/join/action",
					), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return mockCheckAccessClient, nil
			},
		},
		"https://management.azure.com/.default",
	)

	err := validation.Validate(ctx, subscription, cluster)
	assert.NoError(t, err)
	// NSG + VNet + RouteTable = 3 CheckAccess calls
	assert.Equal(t, 3, checkAccessCallCount)
}
