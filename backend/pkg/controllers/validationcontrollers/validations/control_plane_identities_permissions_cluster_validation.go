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
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azurehelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ControlPlaneIdentitiesPermissionsClusterValidation validates that the control plane identities have the necessary permissions.
type ControlPlaneIdentitiesPermissionsClusterValidation struct {
	smiClientBuilder                            azureclient.ServiceManagedIdentityClientBuilder
	clusterScopedIdentitiesConfig               *azure.ClusterScopedIdentitiesConfig
	backendIdentityAzureCachedReaders           *cachedreader.BackendIdentityAzureCachedReaders
	checkAccessV2ClientBuilder                  azureclient.CheckAccessV2ClientBuilder
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	// checkAccessV2Scope is the OAuth scope (typically a "<resource>/.default" App ID URI. public, gov, and China clouds
	// each use a different App ID URI respectively) passed to MI Dataplane when minting an access token for each control plane operator identity.
	checkAccessV2Scope string
}

func NewControlPlaneIdentitiesPermissionsClusterValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	backendIdentityAzureCachedReaders *cachedreader.BackendIdentityAzureCachedReaders,
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder,
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
	checkAccessV2Scope string,
) *ControlPlaneIdentitiesPermissionsClusterValidation {
	return &ControlPlaneIdentitiesPermissionsClusterValidation{
		smiClientBuilder:                            smiClientBuilder,
		clusterScopedIdentitiesConfig:               clusterScopedIdentitiesConfig,
		backendIdentityAzureCachedReaders:           backendIdentityAzureCachedReaders,
		checkAccessV2ClientBuilder:                  checkAccessV2ClientBuilder,
		miDataplaneBasedAccessTokenRetrieverBuilder: miDataplaneBasedAccessTokenRetrieverBuilder,
		checkAccessV2Scope:                          checkAccessV2Scope,
	}
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) Name() string {
	return "ControlPlaneIdentitiesPermissionsClusterValidation"
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) error {
	checkAccessV2Client, err := v.checkAccessV2ClientBuilder.Build(*clusterSubscription.Properties.TenantId)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build check access client: %w", err))
	}

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subnets client: %w", err))
	}

	// Fetch the subnet details to validate attached subnet devices permissions.
	subnetResourceID := cluster.CustomerProperties.Platform.SubnetID
	subnet, err := subnetsClient.Get(ctx, subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subnet: %w", err))
	}

	var missingPermissions []*identityResourceMissingPermissions
	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		results, err := v.findMissingActionsForIdentity(ctx, checkAccessV2Client, cluster, operatorName, identity, &subnet.Subnet)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to find missing actions for identity %q: %w", operatorName, err))
		}
		missingPermissions = append(missingPermissions, results...)
	}

	if len(missingPermissions) > 0 {
		jsonBytes, err := json.Marshal(missingPermissions)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to marshal missing permissions: %w", err))
		}
		return utils.TrackError(fmt.Errorf("control plane operators missing required permissions: %s", string(jsonBytes)))
	}

	return nil
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) roleActionsForOperator(ctx context.Context, operatorName string) ([]string, error) {
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)].RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for operator identity %q", operatorName))
	}
	roleDefinitions, err := v.fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs)
	if err != nil {
		return nil, err
	}
	return azurehelpers.UnionActions(roleDefinitions)
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) roleDataActionsForOperator(ctx context.Context, operatorName string) ([]string, error) {
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)].RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, nil
	}
	roleDefinitions, err := v.fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs)
	if err != nil {
		return nil, err
	}
	return azurehelpers.UnionDataActions(roleDefinitions)
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) fetchRoleDefinitions(ctx context.Context, resourceIDs []*azcorearm.ResourceID) ([]armauthorization.RoleDefinition, error) {
	roleDefinitions := make([]armauthorization.RoleDefinition, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		response, err := v.backendIdentityAzureCachedReaders.RoleDefinitionsCachedReader.GetCachedByID(ctx, resourceID.String(), nil)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get role definition %q: %w", resourceID.String(), err))
		}
		roleDefinitions = append(roleDefinitions, response.RoleDefinition)
	}
	return roleDefinitions, nil
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) accessTokenForIdentity(ctx context.Context, clusterIdentityURL string, identity *azcorearm.ResourceID) (azcore.AccessToken, error) {
	retriever, err := v.miDataplaneBasedAccessTokenRetrieverBuilder.Build(clusterIdentityURL, identity)
	if err != nil {
		return azcore.AccessToken{}, utils.TrackError(err)
	}
	token, err := retriever.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{v.checkAccessV2Scope}})
	if err != nil {
		return azcore.AccessToken{}, utils.TrackError(err)
	}
	return token, nil
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) findMissingActionsForIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, cluster *api.HCPOpenShiftCluster, operatorName string, identity *azcorearm.ResourceID, clusterSubnet *armnetwork.Subnet) ([]*identityResourceMissingPermissions, error) {
	roleActions, err := v.roleActionsForOperator(ctx, operatorName)
	if err != nil {
		return nil, err
	}

	roleDataActions, err := v.roleDataActionsForOperator(ctx, operatorName)
	if err != nil {
		return nil, err
	}

	token, err := v.accessTokenForIdentity(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, identity)
	if err != nil {
		return nil, err
	}

	var results []*identityResourceMissingPermissions

	nsgResult, err := v.checkMissingPermissionsForNetworkSecurityGroup(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.NetworkSecurityGroupID, identity, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if nsgResult != nil {
		results = append(results, nsgResult)
	}

	vnetResult, err := v.checkMissingPermissionsForVNet(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.SubnetID, identity, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if vnetResult != nil {
		results = append(results, vnetResult)
	}

	rtResult, err := v.checkMissingPermissionsForRouteTable(ctx, checkAccessV2Client, clusterSubnet, identity, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if rtResult != nil {
		results = append(results, rtResult)
	}

	return results, nil
}

// checkMissingPermissionsForNetworkSecurityGroup checks whether the given identity has all required permissions on the given network security group.
// Only actions from roleActions/roleDataActions that are relevant to NSG resources are checked (via intersection with the known NSG action set); actions irrelevant to NSGs are skipped.
// It returns:
//   - (nil, nil) if the identity has all required permissions, or if none of the role's actions apply to NSG resources.
//   - a non-nil *IdentityResourceMissingPermissions populated with the NSG resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkMissingPermissionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, nsgID *azcorearm.ResourceID, identity *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  nsgID,
		Identity:  identity,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForVNet checks whether the given identity has all required permissions on the VNet that contains the cluster subnet. The VNet resource ID is derived from the subnet ID's parent.
// Only actions from roleActions/roleDataActions that are relevant to VNet resources are checked (via intersection with the known VNet action set); actions irrelevant to VNets are skipped.
// It returns:
//   - (nil, nil) if the identity has all required permissions, or if none of the role's actions apply to VNet resources.
//   - a non-nil *IdentityResourceMissingPermissions populated with the VNet resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkMissingPermissionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnetID *azcorearm.ResourceID, identity *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	vnetResourceID := subnetID.Parent
	decisions, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  vnetResourceID,
		Identity:  identity,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForRouteTable checks whether the given identity has all required permissions on the route table attached to the cluster subnet. If the subnet has no route table, it is a no-op.
// Only actions from roleActions/roleDataActions that are relevant to route table resources are checked (via intersection with the known route table action set).
// It returns:
//   - (nil, nil) if the subnet has no attached route table, if the identity has all required permissions, or if none of the role's actions apply to route table resources.
//   - a non-nil *IdentityResourceMissingPermissions populated with the route table resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the route table resource ID cannot be parsed or the CheckAccessV2 API call fails.
func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkMissingPermissionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, clusterSubnet *armnetwork.Subnet, identity *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	if clusterSubnet.Properties.RouteTable == nil {
		return nil, nil
	}
	routeTableResourceID, err := azcorearm.ParseResourceID(*clusterSubnet.Properties.RouteTable.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
	}
	decisions, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client, clusterSubnet.Properties.RouteTable, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  routeTableResourceID,
		Identity:  identity,
		Decisions: decisions,
	}, nil
}

// checkNotAllowedAndDeniedActionsForNetworkSecurityGroup checks whether the identity represented by the access token has all required permissions on the given network security group resource.
// Only actions from roleDefinitionActions/roleDefinitionDataActions that are relevant to NSG resources are checked (via intersection with the known NSG action set); unrelated actions are
// ignored. This avoids sending actions to CheckAccessV2 that a given operator role was never expected to hold on an NSG.
// It returns:
//   - (nil, nil) if the identity has all required permissions, or if none of the role's actions apply to NSG resources.
//   - a non-nil slice of NotAllowed/Denied AuthorizationDecision entries, one per missing action, if any permission is absent.
//   - (nil, error) if the CheckAccessV2 API call fails.
func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/networkSecurityGroups/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	networkSecurityGroupActions := []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
	var networkSecurityGroupDataActions []string

	requiredActions := azurehelpers.IntersectActions(networkSecurityGroupActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(networkSecurityGroupDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, requiredActions, requiredDataActions, token)
}

// checkNotAllowedAndDeniedActionsForResourceID checks whether the identity represented by the access token has permission to perform the specified `actions` and `dataActions` on the given `resourceID` using the
// CheckAccessV2 API. Regular actions and data actions are combined into a single API call; data actions are sent with IsDataAction=true so the PDP evaluates them against dataAction grants.
//
// CheckAccessV2 returns a per-action AccessDecision. Only Allowed means the identity has the required permission. The function name reflects the two failure outcomes it collects:
//   - NotAllowed: no role assignment grants the action at the requested scope (implicit deny).
//   - Denied: an explicit Azure deny assignment blocks the action, overriding any role grant.
//
// It returns:
// - a slice of AuthorizationDecision entries with AccessDecision of NotAllowed or Denied
// - a nil slice if all actions are explicitly allowed
// - an error if the CheckAccessV2 API call fails or returns an unexpected result
func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForResourceID(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, actions []string, dataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	totalExpected := len(actions) + len(dataActions)
	if totalExpected == 0 {
		return nil, nil
	}

	authRequest, err := checkAccessV2Client.CreateAuthorizationRequest(resourceID.String(), actions, token.Token)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	for _, da := range dataActions {
		authRequest.Actions = append(authRequest.Actions, azurecheckaccessv2client.ActionInfo{
			Id:           da,
			IsDataAction: true,
		})
	}

	authDecisionResponse, err := checkAccessV2Client.CheckAccess(ctx, *authRequest)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if authDecisionResponse == nil {
		return nil, utils.TrackError(fmt.Errorf("authorization response for '%s' is unexpectedly nil, indicating a possible internal failure", resourceID.String()))
	}

	if totalExpected != len(authDecisionResponse.Value) {
		err := fmt.Errorf("mismatch in authorization decision response for '%s': expected '%d' actions, got '%d' actions", resourceID.String(), totalExpected, len(authDecisionResponse.Value))
		return nil, utils.TrackError(err)
	}

	notAllowedAndDeniedActions := v.collectNotAllowedAndDeniedActions(authDecisionResponse.Value)
	return notAllowedAndDeniedActions, nil
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceId *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/virtualNetworks/* and Microsoft.Network/virtualNetworks/subnets/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	subnetActions := []string{
		"Microsoft.Network/virtualNetworks/join/action",
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
	}
	var subnetDataActions []string

	requiredActions := azurehelpers.IntersectActions(subnetActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(subnetDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceId, requiredActions, requiredDataActions, token)
}

func (v *ControlPlaneIdentitiesPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, routeTable *armnetwork.RouteTable, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/routeTables/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	routeTableActions := []string{
		"Microsoft.Network/routeTables/join/action",
	}
	var routeTableDataActions []string

	requiredActions := azurehelpers.IntersectActions(routeTableActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(routeTableDataActions, roleDefinitionDataActions)

	routeTableResourceID, err := azcorearm.ParseResourceID(*routeTable.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, routeTableResourceID, requiredActions, requiredDataActions, token)
}

// collectNotAllowedAndDeniedActions returns CheckAccessV2 decisions where access was not granted. See checkNotAllowedAndDeniedActionsForResourceID for the difference between NotAllowed and Denied.
func (v *ControlPlaneIdentitiesPermissionsClusterValidation) collectNotAllowedAndDeniedActions(authDecisionsResponse []azurecheckaccessv2client.AuthorizationDecision) []*checkaccessv2AuthorizationDecisionData {
	var missingPermissions []*checkaccessv2AuthorizationDecisionData
	for _, authDecision := range authDecisionsResponse {
		if authDecision.AccessDecision == azurecheckaccessv2client.NotAllowed || authDecision.AccessDecision == azurecheckaccessv2client.Denied {
			missingPermissions = append(missingPermissions, &checkaccessv2AuthorizationDecisionData{
				ActionID:       authDecision.ActionId,
				IsDataAction:   authDecision.IsDataAction,
				AccessDecision: authDecision.AccessDecision,
			})
		}
	}

	return missingPermissions
}
