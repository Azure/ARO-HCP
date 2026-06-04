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
	"github.com/Azure/checkaccess-v2-go-sdk/client"

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
	checkAccessClient, err := v.checkAccessV2ClientBuilder.Build(*clusterSubscription.Properties.TenantId)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build check access client: %w", err))
	}

	subnetClient, err := v.smiClientBuilder.SubnetsClient(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subnet client: %w", err))
	}

	// Fetch the subnet details to validate attached subnet devices permissions.
	subnetResourceId := cluster.CustomerProperties.Platform.SubnetID
	subnet, err := subnetClient.Get(ctx, subnetResourceId.ResourceGroupName, subnetResourceId.Parent.Name,
		subnetResourceId.Name, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subnet: %w", err))
	}

	controlPlaneMissingActions := make(map[string][]string)
	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		missingActions, err := v.findMissingActionsForIdentity(ctx, checkAccessClient, cluster, operatorName, identity, subnet)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to find missing actions for identity %q: %w", operatorName, err))
		}
		if len(missingActions) > 0 {
			controlPlaneMissingActions[operatorName] = missingActions
		}
	}

	if len(controlPlaneMissingActions) > 0 {
		return utils.TrackError(fmt.Errorf("control plane operators missing required permissions: %v",
			controlPlaneMissingActions))
	}

	return nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) findMissingActionsForIdentity(ctx context.Context, checkAccessClient azureclient.CheckAccessV2Client, cluster *api.HCPOpenShiftCluster, operatorName string, identity *azcorearm.ResourceID, subnet armnetwork.SubnetsClientGetResponse) ([]string, error) {
	var missingActions []string
	var roleActions []string
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)].RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for operator identity %q", operatorName))
	}
	seenActions := map[string]struct{}{}
	for _, roleDefinitionResourceID := range roleDefinitionsResourceIDs {
		roleDefinition, err := v.backendIdentityAzureCachedReaders.RoleDefinitionsCachedReader.GetCachedByID(ctx, roleDefinitionResourceID.String(), nil)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get role definition %q: %w", roleDefinitionResourceID.String(), err))
		}

		actions, err := azurehelpers.ActionsFromRoleDefinition(roleDefinition.RoleDefinition)
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
	if len(roleActions) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role actions resolved for operator identity %q", operatorName))
	}

	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	retriever, err := v.miDataplaneBasedAccessTokenRetrieverBuilder.Build(clusterIdentityURL, identity)
	if err != nil {
		return missingActions, utils.TrackError(err)
	}
	token, err := retriever.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{v.checkAccessV2Scope}})
	if err != nil {
		return missingActions, utils.TrackError(err)
	}

	// Validate security group permissions
	notAllowedAndDeniedActions, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessClient,
		cluster.CustomerProperties.Platform.NetworkSecurityGroupID, roleActions, token)
	if err != nil {
		return missingActions, utils.TrackError(err)
	}
	if len(notAllowedAndDeniedActions) > 0 {
		missingActions = append(missingActions,
			fmt.Sprintf("Identity '%s' is missing required actions on: %s", identity.String(),
				v.formatMissingRequiredActionsMessage(cluster.CustomerProperties.Platform.NetworkSecurityGroupID.String(),
					notAllowedAndDeniedActions)))
	}

	// Validate VNET permissions
	subnetID := cluster.CustomerProperties.Platform.SubnetID
	if subnetID == nil || subnetID.Parent == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet ID is missing or has no parent VNet"))
	}
	vnetResourceId := subnetID.Parent
	notAllowedAndDeniedActions, err = v.checkNotAllowedAndDeniedActionsForVnet(ctx,
		checkAccessClient, vnetResourceId, roleActions, token)
	if err != nil {
		return missingActions, utils.TrackError(err)
	}
	if len(notAllowedAndDeniedActions) > 0 {
		missingActions = append(missingActions,
			fmt.Sprintf("Identity '%s' is missing required actions on: %s", identity.String(),
				v.formatMissingRequiredActionsMessage(vnetResourceId.String(),
					notAllowedAndDeniedActions)))
	}

	// Validate subnet attached devices permissions.
	if subnet.Properties != nil && subnet.Properties.RouteTable != nil {
		notAllowedAndDeniedActions, err = v.checkNotAllowedAndDeniedActionsForRouteTable(ctx,
			checkAccessClient, subnet.Properties.RouteTable, roleActions, token)
		if err != nil {
			return missingActions, utils.TrackError(err)
		}
		if len(notAllowedAndDeniedActions) > 0 {
			missingActions = append(missingActions,
				fmt.Sprintf("Identity '%s' is missing required actions on: %s", identity.String(),
					v.formatMissingRequiredActionsMessage(*subnet.Properties.RouteTable.ID,
						notAllowedAndDeniedActions)))
		}
	}

	return missingActions, nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx context.Context, checkAccessClient azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, token azcore.AccessToken) ([]client.AuthorizationDecision, error) {
	// Minimal set of required actions for the network security group.
	networkSecurityGroupActions := []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
	requiredActions := azurehelpers.IntersectActions(networkSecurityGroupActions, roleDefinitionActions)
	if len(requiredActions) == 0 {
		return nil, nil
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessClient, resourceID, requiredActions, token)
}

// checkNotAllowedAndDeniedActionsForResourceID checks whether the identity represented by the access token
// has permission to perform the specified `actions` on the given `resourceId` using the CheckAccessV2 API.
//
// It returns:
// - a slice of AuthorizationDecision entries with AccessDecision of NotAllowed or Denied
// - a nil slice if all actions are explicitly allowed
// - an error if the CheckAccess API call fails or returns an unexpected result
func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForResourceID(ctx context.Context, checkAccessClient azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, actions []string, token azcore.AccessToken) ([]client.AuthorizationDecision, error) {
	logger := utils.LoggerFromContext(ctx)

	authRequest, err := checkAccessClient.CreateAuthorizationRequest(resourceID.String(), actions, token.Token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	authDecisionResponse, err := checkAccessClient.CheckAccess(ctx, *authRequest)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if authDecisionResponse == nil {
		return nil, utils.TrackError(fmt.Errorf("authorization response for '%s' is unexpectedly nil, "+
			"indicating a possible internal failure", resourceID.String()))
	}

	if len(actions) != len(authDecisionResponse.Value) {
		logger.Error(fmt.Errorf("mismatch in authorization decision response for '%s': "+
			"expected '%d' actions, got '%d' actions",
			resourceID.String(), len(actions), len(authDecisionResponse.Value)), "mismatch in authorization decision response")
	}

	notAllowedAndDeniedActions := v.filterNotAllowedAndDeniedActions(authDecisionResponse.Value)
	return notAllowedAndDeniedActions, nil
}

func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForVnet(ctx context.Context, checkAccessClient azureclient.CheckAccessV2Client, resourceId *azcorearm.ResourceID, roleDefinitionActions []string, token azcore.AccessToken) ([]client.AuthorizationDecision, error) {
	// Minimal set of required actions for the virtual network.
	subnetActions := []string{
		"Microsoft.Network/virtualNetworks/join/action",
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
	}
	requiredActions := azurehelpers.IntersectActions(subnetActions, roleDefinitionActions)
	if len(requiredActions) == 0 {
		return nil, nil
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessClient, resourceId, requiredActions, token)
}

func (v *ControlPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForRouteTable(ctx context.Context, checkAccessClient azureclient.CheckAccessV2Client, routeTable *armnetwork.RouteTable, roleDefinitionActions []string, token azcore.AccessToken) ([]client.AuthorizationDecision, error) {
	// Minimal set of required actions for the route table.
	routeTableActions := []string{
		"Microsoft.Network/routeTables/join/action",
	}
	requiredActions := azurehelpers.IntersectActions(routeTableActions, roleDefinitionActions)
	if len(requiredActions) == 0 {
		return nil, nil
	}

	routeTableResourceID, err := azcorearm.ParseResourceID(*routeTable.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessClient, routeTableResourceID, requiredActions, token)
}

// filterNotAllowedAndDeniedActions filters out only those authorization decisions where access was not granted.
// These are either explicitly Denied or simply NotAllowed.
func (v *ControlPlaneIdentitiesPermissionsValidation) filterNotAllowedAndDeniedActions(authDecisionsResponse []client.AuthorizationDecision) []client.AuthorizationDecision {
	var missingPermissions []client.AuthorizationDecision
	for _, authDecision := range authDecisionsResponse {
		if authDecision.AccessDecision == client.NotAllowed || authDecision.AccessDecision == client.Denied {
			missingPermissions = append(missingPermissions, authDecision)
		}
	}

	return missingPermissions
}

func (v *ControlPlaneIdentitiesPermissionsValidation) formatMissingRequiredActionsMessage(resourceId string, notAllowedAndDeniedActions []client.AuthorizationDecision) string {
	var notAllowedActions []string
	var deniedActions []string
	for _, action := range notAllowedAndDeniedActions {
		switch action.AccessDecision {
		case client.NotAllowed:
			notAllowedActions = append(notAllowedActions, action.ActionId)
		case client.Denied:
			deniedActions = append(deniedActions, action.ActionId)
		}
	}

	message := fmt.Sprintf("resource ID '%s':", resourceId)
	if len(notAllowedActions) > 0 {
		message += fmt.Sprintf(" not allowed: %s", strings.Join(notAllowedActions, ", "))
	}
	if len(deniedActions) > 0 {
		message += fmt.Sprintf(" denied: %s", strings.Join(deniedActions, ", "))
	}

	return message
}
