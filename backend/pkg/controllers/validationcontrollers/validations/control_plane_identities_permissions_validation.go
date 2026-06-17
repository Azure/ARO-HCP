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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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

// ControlPlaneIdentitiesPermissionsValidation validates that the control plane identities have the necessary permissions.
type ControlPlaneIdentitiesPermissionsValidation struct {
	smiClientBuilder                            azureclient.ServiceManagedIdentityClientBuilder
	clusterScopedIdentitiesConfig               *azure.ClusterScopedIdentitiesConfig
	backendIdentityAzureCachedReaders           *cachedreader.BackendIdentityAzureCachedReaders
	checkAccessV2ClientBuilder                  azureclient.CheckAccessV2ClientBuilder
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	// checkAccessV2Scope is the OAuth scope (typically a "<resource>/.default" App ID URI. public, gov, and China clouds
	// each use a different App ID URI respectively) passed to MI Dataplane when minting an access token for each control plane operator identity.
	checkAccessV2Scope string
}

func NewControlPlaneIdentitiesPermissionsValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	backendIdentityAzureCachedReaders *cachedreader.BackendIdentityAzureCachedReaders,
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder,
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
	checkAccessV2Scope string,
) *ControlPlaneIdentitiesPermissionsValidation {
	return &ControlPlaneIdentitiesPermissionsValidation{
		smiClientBuilder:                            smiClientBuilder,
		clusterScopedIdentitiesConfig:               clusterScopedIdentitiesConfig,
		backendIdentityAzureCachedReaders:           backendIdentityAzureCachedReaders,
		checkAccessV2ClientBuilder:                  checkAccessV2ClientBuilder,
		miDataplaneBasedAccessTokenRetrieverBuilder: miDataplaneBasedAccessTokenRetrieverBuilder,
		checkAccessV2Scope:                          checkAccessV2Scope,
	}
}

func (v *ControlPlaneIdentitiesPermissionsValidation) Name() string {
	return "ControlPlaneIdentitiesPermissionsValidation"
}

func (v *ControlPlaneIdentitiesPermissionsValidation) Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) error {
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

	var missingPermissions []IdentityResourceMissingPermissions
	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		results, err := v.findMissingActionsForIdentity(ctx, checkAccessV2Client, cluster, operatorName, identity, &subnet.Subnet)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to find missing actions for identity %q: %w", operatorName, err))
		}
		missingPermissions = append(missingPermissions, results...)
	}

	if len(missingPermissions) > 0 {
		return utils.TrackError(fmt.Errorf("control plane operators missing required permissions: %s",
			formatMissingPermissionsMessage(missingPermissions)))
	}

	return nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) roleActionsForOperator(ctx context.Context, operatorName string) ([]string, error) {
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)].RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for operator identity %q", operatorName))
	}
	var roleActions []string
	seenActions := map[string]struct{}{}
	for _, roleDefinitionResourceID := range roleDefinitionsResourceIDs {
		roleDefinitionResponse, err := v.backendIdentityAzureCachedReaders.RoleDefinitionsCachedReader.GetCachedByID(ctx, roleDefinitionResourceID.String(), nil)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get role definition %q: %w", roleDefinitionResourceID.String(), err))
		}

		actions, err := azurehelpers.ActionsFromRoleDefinition(roleDefinitionResponse.RoleDefinition)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		for _, action := range actions {
			if _, ok := seenActions[action]; ok {
				continue
			}
			seenActions[action] = struct{}{}
			roleActions = append(roleActions, action)
		}
	}
	return roleActions, nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) roleDataActionsForOperator(ctx context.Context, operatorName string) ([]string, error) {
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)].RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, nil
	}
	var roleDataActions []string
	seenDataActions := map[string]struct{}{}
	for _, roleDefinitionResourceID := range roleDefinitionsResourceIDs {
		roleDefinitionResponse, err := v.backendIdentityAzureCachedReaders.RoleDefinitionsCachedReader.GetCachedByID(ctx, roleDefinitionResourceID.String(), nil)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get role definition %q: %w", roleDefinitionResourceID.String(), err))
		}

		dataActions, err := azurehelpers.DataActionsFromRoleDefinition(roleDefinitionResponse.RoleDefinition)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		for _, dataAction := range dataActions {
			if _, ok := seenDataActions[dataAction]; ok {
				continue
			}
			seenDataActions[dataAction] = struct{}{}
			roleDataActions = append(roleDataActions, dataAction)
		}
	}

	return roleDataActions, nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) accessTokenForIdentity(ctx context.Context, clusterIdentityURL string, identity *azcorearm.ResourceID) (azcore.AccessToken, error) {
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

func (v *ControlPlaneIdentitiesPermissionsValidation) findMissingActionsForIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, cluster *api.HCPOpenShiftCluster, operatorName string, identity *azcorearm.ResourceID, clusterSubnet *armnetwork.Subnet) ([]IdentityResourceMissingPermissions, error) {
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

	var results []IdentityResourceMissingPermissions

	// Validate security group permissions
	nsgDecisions, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.NetworkSecurityGroupID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(nsgDecisions) > 0 {
		results = append(results, IdentityResourceMissingPermissions{
			Resource:  cluster.CustomerProperties.Platform.NetworkSecurityGroupID,
			Identity:  identity,
			Decisions: nsgDecisions,
		})
	}

	vnetResult, err := v.validateVnetPermissions(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.SubnetID, identity, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if vnetResult != nil {
		results = append(results, *vnetResult)
	}

	rtResult, err := v.validateRouteTablePermissions(ctx, checkAccessV2Client, clusterSubnet, identity, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if rtResult != nil {
		results = append(results, *rtResult)
	}

	return results, nil
}

// validateVnetPermissions checks whether the given identity has all required permissions on the VNet that contains the cluster subnet. The VNet resource ID is derived from the subnet ID's parent.
// Only actions from roleActions/roleDataActions that are relevant to VNet resources are checked (via intersection with the known VNet action set); actions irrelevant to VNets are skipped.
// It returns:
//   - (nil, nil) if the identity has all required permissions, or if none of the role's actions apply to VNet resources.
//   - a non-nil *IdentityResourceMissingPermissions populated with the VNet resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the CheckAccessV2 API call fails.
func (v *ControlPlaneIdentitiesPermissionsValidation) validateVnetPermissions(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnetID *azcorearm.ResourceID, identity *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*IdentityResourceMissingPermissions, error) {
	vnetResourceID := subnetID.Parent
	decisions, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &IdentityResourceMissingPermissions{
		Resource:  vnetResourceID,
		Identity:  identity,
		Decisions: decisions,
	}, nil
}

// validateRouteTablePermissions checks whether the given identity has all required permissions on the route table attached to the cluster subnet. If the subnet has no route table, it is a no-op.
// Only actions from roleActions/roleDataActions that are relevant to route table resources are checked (via intersection with the known route table action set).
// It returns:
//   - (nil, nil) if the subnet has no attached route table, if the identity has all required permissions, or if none of the role's actions apply to route table resources.
//   - a non-nil *IdentityResourceMissingPermissions populated with the route table resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the route table resource ID cannot be parsed or the CheckAccessV2 API call fails.
func (v *ControlPlaneIdentitiesPermissionsValidation) validateRouteTablePermissions(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, clusterSubnet *armnetwork.Subnet, identity *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*IdentityResourceMissingPermissions, error) {
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
	return &IdentityResourceMissingPermissions{
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
func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]azurecheckaccessv2client.AuthorizationDecision, error) {
	networkSecurityGroupActions := []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
	var networkSecurityGroupDataActions []string

	requiredActions := azurehelpers.IntersectActions(networkSecurityGroupActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(networkSecurityGroupDataActions, roleDefinitionDataActions)
	if len(requiredActions) == 0 && len(requiredDataActions) == 0 {
		return nil, nil
	}

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
func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForResourceID(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, actions []string, dataActions []string, token azcore.AccessToken) ([]azurecheckaccessv2client.AuthorizationDecision, error) {
	logger := utils.LoggerFromContext(ctx)

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
		return nil, utils.TrackError(fmt.Errorf("authorization response for '%s' is unexpectedly nil, "+
			"indicating a possible internal failure", resourceID.String()))
	}

	if totalExpected != len(authDecisionResponse.Value) {
		logger.Error(fmt.Errorf("mismatch in authorization decision response for '%s': "+
			"expected '%d' actions, got '%d' actions",
			resourceID.String(), totalExpected, len(authDecisionResponse.Value)), "mismatch in authorization decision response")
	}

	notAllowedAndDeniedActions := v.collectNotAllowedAndDeniedActions(authDecisionResponse.Value)
	return notAllowedAndDeniedActions, nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceId *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]azurecheckaccessv2client.AuthorizationDecision, error) {
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
	if len(requiredActions) == 0 && len(requiredDataActions) == 0 {
		return nil, nil
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceId, requiredActions, requiredDataActions, token)
}

func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, routeTable *armnetwork.RouteTable, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]azurecheckaccessv2client.AuthorizationDecision, error) {
	routeTableActions := []string{
		"Microsoft.Network/routeTables/join/action",
	}
	var routeTableDataActions []string

	requiredActions := azurehelpers.IntersectActions(routeTableActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(routeTableDataActions, roleDefinitionDataActions)
	if len(requiredActions) == 0 && len(requiredDataActions) == 0 {
		return nil, nil
	}

	routeTableResourceID, err := azcorearm.ParseResourceID(*routeTable.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, routeTableResourceID, requiredActions, requiredDataActions, token)
}

// collectNotAllowedAndDeniedActions returns CheckAccessV2 decisions where access was not granted.
// See checkNotAllowedAndDeniedActionsForResourceID for the difference between NotAllowed and Denied.
func (v *ControlPlaneIdentitiesPermissionsValidation) collectNotAllowedAndDeniedActions(authDecisionsResponse []azurecheckaccessv2client.AuthorizationDecision) []azurecheckaccessv2client.AuthorizationDecision {
	var missingPermissions []azurecheckaccessv2client.AuthorizationDecision
	for _, authDecision := range authDecisionsResponse {
		if authDecision.AccessDecision == azurecheckaccessv2client.NotAllowed || authDecision.AccessDecision == azurecheckaccessv2client.Denied {
			missingPermissions = append(missingPermissions, authDecision)
		}
	}

	return missingPermissions
}

// formatMissingPermissionsMessage builds a human-readable string from a set of authorization
// failures. Each IdentityResourceMissingPermissions contributes one entry describing the
// identity, resource, and the specific not-allowed and denied actions or dataActions, for example:
//
//	identity '.../cloud-controller' on resource '.../networkSecurityGroups/nsg': not allowed actions: Microsoft.Network/networkSecurityGroups/write, denied dataActions: Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read
func formatMissingPermissionsMessage(results []IdentityResourceMissingPermissions) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		var notAllowedActions []string
		var notAllowedDataActions []string
		var deniedActions []string
		var deniedDataActions []string
		for _, decision := range result.Decisions {
			switch decision.AccessDecision {
			case azurecheckaccessv2client.NotAllowed:
				if decision.IsDataAction {
					notAllowedDataActions = append(notAllowedDataActions, decision.ActionId)
				} else {
					notAllowedActions = append(notAllowedActions, decision.ActionId)
				}
			case azurecheckaccessv2client.Denied:
				if decision.IsDataAction {
					deniedDataActions = append(deniedDataActions, decision.ActionId)
				} else {
					deniedActions = append(deniedActions, decision.ActionId)
				}
			}
		}
		msg := fmt.Sprintf("identity '%s' on resource '%s':", result.Identity.String(), result.Resource.String())
		if len(notAllowedActions) > 0 {
			msg += fmt.Sprintf(" not allowed actions: %s", strings.Join(notAllowedActions, ", "))
		}
		if len(notAllowedDataActions) > 0 {
			msg += fmt.Sprintf(" not allowed dataActions: %s", strings.Join(notAllowedDataActions, ", "))
		}
		if len(deniedActions) > 0 {
			msg += fmt.Sprintf(" denied actions: %s", strings.Join(deniedActions, ", "))
		}
		if len(deniedDataActions) > 0 {
			msg += fmt.Sprintf(" denied dataActions: %s", strings.Join(deniedDataActions, ", "))
		}
		parts = append(parts, msg)
	}
	return strings.Join(parts, "; ")
}
