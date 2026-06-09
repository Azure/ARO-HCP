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
	subnetsClientFn func(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) (azureclient.SubnetsClient, error)
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

// mockMIDataplaneBasedIdentityAccessTokenRetriever allows tests to inject an arbitrary
// GetToken response for a specific identity without any MI Dataplane network interaction.
type mockMIDataplaneBasedIdentityAccessTokenRetriever struct {
	getTokenFn func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error)
}

func (m *mockMIDataplaneBasedIdentityAccessTokenRetriever) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return m.getTokenFn(ctx, options)
}

// mockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder allows tests to control what
// retriever is returned for each identity without instantiating a real MI Dataplane client.
type mockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder struct {
	buildFn func(clusterIdentityURL string, identityResourceID *azcorearm.ResourceID) (azureclient.MIDataplaneBasedIdentityAccessTokenRetriever, error)
}

func (m *mockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder) Build(clusterIdentityURL string, identityResourceID *azcorearm.ResourceID) (azureclient.MIDataplaneBasedIdentityAccessTokenRetriever, error) {
	return m.buildFn(clusterIdentityURL, identityResourceID)
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

// fakeToken returns a non-expired access token for use in tests.
func fakeToken() azcore.AccessToken {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}
}

// successfulRetriever returns a mock retriever that always yields fakeToken().
func successfulRetriever() *mockMIDataplaneBasedIdentityAccessTokenRetriever {
	return &mockMIDataplaneBasedIdentityAccessTokenRetriever{
		getTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return fakeToken(), nil
		},
	}
}

// mockSuccessfulTokenRetrieverBuilder returns a builder whose Build always returns
// a retriever that issues a valid fake token, with no MI Dataplane interaction.
func mockSuccessfulTokenRetrieverBuilder() *mockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder {
	return &mockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder{
		buildFn: func(_ string, _ *azcorearm.ResourceID) (azureclient.MIDataplaneBasedIdentityAccessTokenRetriever, error) {
			return successfulRetriever(), nil
		},
	}
}

// testIdentitiesConfig builds a ClusterScopedIdentitiesConfig with a single
// "CloudController" operator referencing the given role definition resource ID.
func testIdentitiesConfig(roleDefResourceID *azcorearm.ResourceID) *azure.ClusterScopedIdentitiesConfig {
	return &azure.ClusterScopedIdentitiesConfig{
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
}

// allNetworkActions returns the full set of network role actions used across tests.
func allNetworkActions() []string {
	return []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
		"Microsoft.Network/virtualNetworks/join/action",
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
	}
}

// mockSimpleSubnetsClient returns a mock SubnetsClient that returns a subnet with
// no attached resources, or with the optional RouteTable set.
func mockSimpleSubnetsClient(routeTable *armnetwork.RouteTable) *mockSubnetsClient {
	return &mockSubnetsClient{
		getFn: func(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
			return armnetwork.SubnetsClientGetResponse{
				Subnet: armnetwork.Subnet{
					Properties: &armnetwork.SubnetPropertiesFormat{
						RouteTable: routeTable,
					},
				},
			}, nil
		},
	}
}

// testCheckAccessV2Scope is the CheckAccessV2 scope used in all Validate tests.
const testCheckAccessV2Scope = "https://management.azure.com/.default"

// --- Tests for Validate ---

// TestValidate_AllPermissionsAllowed verifies that Validate returns no error when
// all required permissions for the control plane operator identity are allowed
// by CheckAccess on both NSG and VNet resources.
func TestValidate_AllPermissionsAllowed(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	mockCheckAccessClient := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	validation := NewControlPlaneIdentitiesPermissionsValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return mockSimpleSubnetsClient(nil), nil
			},
		},
		testIdentitiesConfig(roleDefResourceID),
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(allNetworkActions()...), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) { return mockCheckAccessClient, nil },
		},
		mockSuccessfulTokenRetrieverBuilder(),
		testCheckAccessV2Scope,
	)

	err := validation.Validate(ctx, newTestSubscription(), newTestCluster())
	assert.NoError(t, err)
}

// TestValidate_MissingPermissions verifies that Validate returns an error listing
// missing actions when CheckAccess reports NotAllowed or Denied for some required
// actions on the NSG resource.
func TestValidate_MissingPermissions(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	mockCheckAccessClient := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied},
				},
			}, nil
		},
	}

	validation := NewControlPlaneIdentitiesPermissionsValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return mockSimpleSubnetsClient(nil), nil
			},
		},
		testIdentitiesConfig(roleDefResourceID),
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(allNetworkActions()...), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) { return mockCheckAccessClient, nil },
		},
		mockSuccessfulTokenRetrieverBuilder(),
		testCheckAccessV2Scope,
	)

	err := validation.Validate(ctx, newTestSubscription(), newTestCluster())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "control plane operators missing required permissions")
	assert.Contains(t, err.Error(), "not allowed")
	assert.Contains(t, err.Error(), "denied")
}

// TestValidate_GetTokenError verifies that Validate propagates the error when
// MIDataplaneBasedIdentityAccessTokenRetriever.GetToken fails (e.g. the MI Dataplane
// is unreachable or the token endpoint is unavailable).
func TestValidate_GetTokenError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")

	validation := NewControlPlaneIdentitiesPermissionsValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return mockSimpleSubnetsClient(nil), nil
			},
		},
		testIdentitiesConfig(roleDefResourceID),
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse("Microsoft.Network/networkSecurityGroups/read"), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return &mockCheckAccessV2Client{}, nil
			},
		},
		&mockMIDataplaneBasedIdentityAccessTokenRetrieverBuilder{
			buildFn: func(_ string, _ *azcorearm.ResourceID) (azureclient.MIDataplaneBasedIdentityAccessTokenRetriever, error) {
				return &mockMIDataplaneBasedIdentityAccessTokenRetriever{
					getTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{}, errors.New("failed to acquire token from MI dataplane")
					},
				}, nil
			},
		},
		testCheckAccessV2Scope,
	)

	err := validation.Validate(ctx, newTestSubscription(), newTestCluster())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to acquire token from MI dataplane")
}

// TestValidate_SubnetClientError verifies that Validate returns an error when
// the SubnetsClient cannot be created from the SMI client builder.
func TestValidate_SubnetClientError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	validation := NewControlPlaneIdentitiesPermissionsValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return nil, errors.New("subnet client creation failed")
			},
		},
		&azure.ClusterScopedIdentitiesConfig{},
		&cachedreader.BackendIdentityAzureCachedReaders{},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) {
				return &mockCheckAccessV2Client{}, nil
			},
		},
		mockSuccessfulTokenRetrieverBuilder(),
		testCheckAccessV2Scope,
	)

	err := validation.Validate(ctx, newTestSubscription(), newTestCluster())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get subnets client")
}

// TestValidate_CheckAccessClientBuildError verifies that Validate returns an error
// when the CheckAccessV2 client builder fails to construct the client.
func TestValidate_CheckAccessClientBuildError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	validation := NewControlPlaneIdentitiesPermissionsValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
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
		mockSuccessfulTokenRetrieverBuilder(),
		testCheckAccessV2Scope,
	)

	err := validation.Validate(ctx, newTestSubscription(), newTestCluster())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to build check access client")
}

// TestValidate_RouteTablePermissionsChecked verifies that when the subnet has an
// attached route table, Validate also checks permissions on the route table resource,
// resulting in 3 CheckAccess calls total (NSG + VNet + RouteTable).
func TestValidate_RouteTablePermissionsChecked(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	roleDefResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/test-role-def")
	routeTableID := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.Network/routeTables/testRouteTable"

	checkAccessCallCount := 0
	mockCheckAccessClient := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			checkAccessCallCount++
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	roleActions := append(allNetworkActions(), "Microsoft.Network/routeTables/join/action")
	validation := NewControlPlaneIdentitiesPermissionsValidation(
		&mockSMIClientBuilder{
			subnetsClientFn: func(_ context.Context, _ string, _ *azcorearm.ResourceID, _ string) (azureclient.SubnetsClient, error) {
				return mockSimpleSubnetsClient(&armnetwork.RouteTable{ID: ptr.To(routeTableID)}), nil
			},
		},
		testIdentitiesConfig(roleDefResourceID),
		&cachedreader.BackendIdentityAzureCachedReaders{
			RoleDefinitionsCachedReader: &mockRoleDefinitionsCachedReader{
				getCachedByIDFn: func(_ context.Context, _ string, _ *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
					return newRoleDefinitionResponse(roleActions...), nil
				},
			},
		},
		&mockCheckAccessV2ClientBuilder{
			buildFn: func(_ string) (azureclient.CheckAccessV2Client, error) { return mockCheckAccessClient, nil },
		},
		mockSuccessfulTokenRetrieverBuilder(),
		testCheckAccessV2Scope,
	)

	err := validation.Validate(ctx, newTestSubscription(), newTestCluster())
	assert.NoError(t, err)
	// NSG + VNet + RouteTable = 3 CheckAccess calls
	assert.Equal(t, 3, checkAccessCallCount)
}

// --- Tests for checkNotAllowedAndDeniedActionsForNetworkSecurityGroup ---

// TestCheckNotAllowedAndDeniedActionsForNSG_AllAllowed verifies that no denied actions
// are returned when CheckAccess reports Allowed for all NSG actions.
func TestCheckNotAllowedAndDeniedActionsForNSG_AllAllowed(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	nsgResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/networkSecurityGroups/testNSG")

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	result, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgResourceID, allNetworkActions(), fakeToken())
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestCheckNotAllowedAndDeniedActionsForNSG_SomeDenied verifies that denied actions
// are returned when CheckAccess reports NotAllowed or Denied for some NSG actions.
func TestCheckNotAllowedAndDeniedActionsForNSG_SomeDenied(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	nsgResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/networkSecurityGroups/testNSG")

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: checkaccessv2.NotAllowed},
					{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: checkaccessv2.Denied},
				},
			}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	result, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgResourceID, allNetworkActions(), fakeToken())
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, checkaccessv2.NotAllowed, result[0].AccessDecision)
	assert.Equal(t, checkaccessv2.Denied, result[1].AccessDecision)
}

// TestCheckNotAllowedAndDeniedActionsForNSG_NoMatchingRoleActions verifies that when
// the role definition has no NSG-related actions, CheckAccess is not called and no
// results are returned.
func TestCheckNotAllowedAndDeniedActionsForNSG_NoMatchingRoleActions(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	nsgResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/networkSecurityGroups/testNSG")
	checkAccessCalled := false
	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			checkAccessCalled = true
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			checkAccessCalled = true
			return &checkaccessv2.AuthorizationDecisionResponse{}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	// Pass only route table actions — none overlap with NSG actions.
	result, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgResourceID,
		[]string{"Microsoft.Network/routeTables/join/action"}, fakeToken())
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.False(t, checkAccessCalled, "CheckAccess should not be called when there are no matching NSG actions")
}

// TestCheckNotAllowedAndDeniedActionsForNSG_CheckAccessError verifies that errors
// from CheckAccess are propagated to the caller.
func TestCheckNotAllowedAndDeniedActionsForNSG_CheckAccessError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	nsgResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/networkSecurityGroups/testNSG")

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return nil, errors.New("check access API unavailable")
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	_, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgResourceID, allNetworkActions(), fakeToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check access API unavailable")
}

// --- Tests for checkNotAllowedAndDeniedActionsForVNet ---

// TestCheckNotAllowedAndDeniedActionsForVnet_AllAllowed verifies that no denied actions
// are returned when CheckAccess reports Allowed for all VNet actions.
func TestCheckNotAllowedAndDeniedActionsForVnet_AllAllowed(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	vnetResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/virtualNetworks/testVNet")

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/virtualNetworks/read", AccessDecision: checkaccessv2.Allowed},
					{ActionId: "Microsoft.Network/virtualNetworks/subnets/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	result, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, allNetworkActions(), fakeToken())
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestCheckNotAllowedAndDeniedActionsForVnet_SomeDenied verifies that denied actions
// are returned when CheckAccess reports NotAllowed or Denied for some VNet actions.
func TestCheckNotAllowedAndDeniedActionsForVnet_SomeDenied(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	vnetResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/virtualNetworks/testVNet")

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/virtualNetworks/join/action", AccessDecision: checkaccessv2.Denied},
					{ActionId: "Microsoft.Network/virtualNetworks/read", AccessDecision: checkaccessv2.NotAllowed},
				},
			}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	result, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, allNetworkActions(), fakeToken())
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, checkaccessv2.Denied, result[0].AccessDecision)
	assert.Equal(t, checkaccessv2.NotAllowed, result[1].AccessDecision)
}

// TestCheckNotAllowedAndDeniedActionsForVnet_NoMatchingRoleActions verifies that when
// the role definition has no VNet-related actions, CheckAccess is not called.
func TestCheckNotAllowedAndDeniedActionsForVnet_NoMatchingRoleActions(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	vnetResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/virtualNetworks/testVNet")
	checkAccessCalled := false
	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			checkAccessCalled = true
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			checkAccessCalled = true
			return &checkaccessv2.AuthorizationDecisionResponse{}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	// Pass only NSG actions — none overlap with VNet actions.
	result, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID,
		[]string{"Microsoft.Network/networkSecurityGroups/read"}, fakeToken())
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.False(t, checkAccessCalled, "CheckAccess should not be called when there are no matching VNet actions")
}

// TestCheckNotAllowedAndDeniedActionsForVnet_CheckAccessError verifies that errors
// from CheckAccess are propagated to the caller.
func TestCheckNotAllowedAndDeniedActionsForVnet_CheckAccessError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	vnetResourceID := mustParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/virtualNetworks/testVNet")

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return nil, errors.New("check access API unavailable")
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	_, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, allNetworkActions(), fakeToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check access API unavailable")
}

// --- Tests for checkNotAllowedAndDeniedActionsForRouteTable ---

// TestCheckNotAllowedAndDeniedActionsForRouteTable_AllAllowed verifies that no denied
// actions are returned when CheckAccess reports Allowed for the route table join action.
func TestCheckNotAllowedAndDeniedActionsForRouteTable_AllAllowed(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	routeTableID := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/routeTables/testRT"

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Allowed},
				},
			}, nil
		},
	}

	roleActions := []string{"Microsoft.Network/routeTables/join/action"}
	v := &ControlPlaneIdentitiesPermissionsValidation{}
	result, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client, &armnetwork.RouteTable{ID: ptr.To(routeTableID)}, roleActions, fakeToken())
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestCheckNotAllowedAndDeniedActionsForRouteTable_Denied verifies that a denied
// action is returned when CheckAccess reports Denied for the route table join action.
func TestCheckNotAllowedAndDeniedActionsForRouteTable_Denied(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	routeTableID := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/routeTables/testRT"

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return &checkaccessv2.AuthorizationDecisionResponse{
				Value: []checkaccessv2.AuthorizationDecision{
					{ActionId: "Microsoft.Network/routeTables/join/action", AccessDecision: checkaccessv2.Denied},
				},
			}, nil
		},
	}

	roleActions := []string{"Microsoft.Network/routeTables/join/action"}
	v := &ControlPlaneIdentitiesPermissionsValidation{}
	result, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client, &armnetwork.RouteTable{ID: ptr.To(routeTableID)}, roleActions, fakeToken())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, checkaccessv2.Denied, result[0].AccessDecision)
}

// TestCheckNotAllowedAndDeniedActionsForRouteTable_NoMatchingRoleActions verifies that
// when the role definition has no route table actions, CheckAccess is not called.
func TestCheckNotAllowedAndDeniedActionsForRouteTable_NoMatchingRoleActions(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	routeTableID := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/routeTables/testRT"
	checkAccessCalled := false
	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			checkAccessCalled = true
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			checkAccessCalled = true
			return &checkaccessv2.AuthorizationDecisionResponse{}, nil
		},
	}

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	// Pass only NSG actions — none overlap with route table actions.
	result, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client,
		&armnetwork.RouteTable{ID: ptr.To(routeTableID)},
		[]string{"Microsoft.Network/networkSecurityGroups/read"}, fakeToken())
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.False(t, checkAccessCalled, "CheckAccess should not be called when there are no matching route table actions")
}

// TestCheckNotAllowedAndDeniedActionsForRouteTable_InvalidResourceID verifies that an
// error is returned when the route table has an unparseable resource ID.
func TestCheckNotAllowedAndDeniedActionsForRouteTable_InvalidResourceID(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	v := &ControlPlaneIdentitiesPermissionsValidation{}
	_, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, &mockCheckAccessV2Client{},
		&armnetwork.RouteTable{ID: ptr.To("not-a-valid-resource-id")},
		[]string{"Microsoft.Network/routeTables/join/action"}, fakeToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse route table resource ID")
}

// TestCheckNotAllowedAndDeniedActionsForRouteTable_CheckAccessError verifies that errors
// from CheckAccess are propagated to the caller.
func TestCheckNotAllowedAndDeniedActionsForRouteTable_CheckAccessError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	routeTableID := "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testRG/providers/Microsoft.Network/routeTables/testRT"

	checkAccessV2Client := &mockCheckAccessV2Client{
		createAuthorizationRequest: func(_ string, _ []string, _ string) (*checkaccessv2.AuthorizationRequest, error) {
			return &checkaccessv2.AuthorizationRequest{}, nil
		},
		checkAccessFn: func(_ context.Context, _ checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
			return nil, errors.New("check access API unavailable")
		},
	}

	roleActions := []string{"Microsoft.Network/routeTables/join/action"}
	v := &ControlPlaneIdentitiesPermissionsValidation{}
	_, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client,
		&armnetwork.RouteTable{ID: ptr.To(routeTableID)}, roleActions, fakeToken())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check access API unavailable")
}
