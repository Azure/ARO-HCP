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
	"encoding/json"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azurehelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DataPlaneIdentitiesPermissionsValidation validates that the cluster's data plane operator identities have the RBAC permissions they need over the user-provided azure resources
// (network security group, VNet, subnet, NAT gateway, and route table).
//
// Unlike ControlPlaneIdentitiesPermissionsClusterValidation, which mints a JWT for each control-plane operator identity via MI Dataplane and passes that token to CheckAccessV2, this validator identifies
// each data-plane operator to CheckAccessV2 by its Entra object ID (PrincipalID from the UAI). MI Dataplane can only mint access tokens for identities that are attached to the cluster's ARM resource
// via its top-level "identity" property (identity.userAssignedIdentities), data-plane operator identities are never passed there, only referenced under properties.platform.operatorsAuthentication, so
// MI Dataplane has no authorization to mint tokens for them. Data-plane operators instead obtain their own tokens via an OAuth/workload-identity-federation flow, so this validator has no JWT to pass to
// CheckAccessV2 and must use the ObjectId subject path instead. See createAuthorizationRequestForDataPlaneIdentity.
//
// Limitation: this ObjectId-only subject means CheckAccessV2 cannot see the identity's Entra group memberships, so this validator only detects permissions granted by role assignments made directly to
// the identity's principal ID. It misses permissions granted via a role assignment on a group the identity belongs to. This is a direct consequence of how CheckAccessV2 resolves group membership, when
// the control-plane path builds its request from a JWT (see the vendored checkaccess-v2-go-sdk's RemotePDPClient.CreateAuthorizationRequest), it populates SubjectAttributes.Groups from the token's
// "groups" claim (or sets ClaimName to trigger server-side group expansion on claims-overage tokens). With only an ObjectId and no token, there are no group claims to forward, and CheckAccessV2 does
// not otherwise expand a bare ObjectId's group membership itself. Users of the service are instructed to grant RBAC roles directly to the cluster data plane operator's identities (as documented for ARO-HCP),
// not via group membership, but it should be kept in mind when interpreting a "no missing permissions" result from this validator.
type DataPlaneIdentitiesPermissionsValidation struct {
	smiClientBuilder                  azureclient.ServiceManagedIdentityClientBuilder
	clusterScopedIdentitiesConfig     *azure.ClusterScopedIdentitiesConfig
	backendIdentityAzureCachedReaders *cachedreader.BackendIdentityAzureCachedReaders
	checkAccessV2ClientBuilder        azureclient.CheckAccessV2ClientBuilder
}

func NewDataPlaneIdentitiesPermissionsValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	backendIdentityAzureCachedReaders *cachedreader.BackendIdentityAzureCachedReaders,
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder,
) *DataPlaneIdentitiesPermissionsValidation {
	return &DataPlaneIdentitiesPermissionsValidation{
		smiClientBuilder:                  smiClientBuilder,
		clusterScopedIdentitiesConfig:     clusterScopedIdentitiesConfig,
		backendIdentityAzureCachedReaders: backendIdentityAzureCachedReaders,
		checkAccessV2ClientBuilder:        checkAccessV2ClientBuilder,
	}
}

func (v *DataPlaneIdentitiesPermissionsValidation) Name() string {
	return "DataPlaneIdentitiesPermissionsValidation"
}

var _ ClusterValidation = (*DataPlaneIdentitiesPermissionsValidation)(nil)

func (v *DataPlaneIdentitiesPermissionsValidation) Validate(ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.HCPOpenShiftCluster) ValidationResult {
	checkAccessV2Client, err := v.checkAccessV2ClientBuilder.Build(*clusterSubscription.Properties.TenantId)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify data plane identities permissions.",
			fmt.Sprintf("failed to build check access client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	userAssignedIdentitiesClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify data plane identities permissions.",
			fmt.Sprintf("failed to get user assigned identities client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify data plane identities permissions.",
			fmt.Sprintf("failed to get subnets client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	// Fetch the subnet details to validate subnet-attached resources (NAT gateway, route table) permissions.
	subnetResourceID := cluster.CustomerProperties.Platform.SubnetID
	subnet, err := subnetsClient.Get(ctx, subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify data plane identities permissions.",
			fmt.Sprintf("failed to get subnet: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}
	if subnet.Properties == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify data plane identities permissions.",
			fmt.Sprintf("subnet properties are nil for subnet %s", subnetResourceID),
			ControllerReportingPolicyTypeError,
		)
	}

	var missingPermissions []*identityResourceMissingPermissions
	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		identityObjectID, err := v.retrieveIdentityObjectID(ctx, userAssignedIdentitiesClient, identity)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify data plane identities permissions.",
				fmt.Sprintf("failed to retrieve identity %s objectID, belonging to operator %s: %v", identity, operatorName, err),
				ControllerReportingPolicyTypeError,
			)
		}

		results, err := v.findMissingActionsForIdentity(ctx, checkAccessV2Client, identityObjectID, cluster, operatorName, identity, &subnet.Subnet)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify data plane identities permissions.",
				fmt.Sprintf("failed to find missing actions for identity %q: %v", operatorName, err),
				ControllerReportingPolicyTypeError,
			)
		}
		missingPermissions = append(missingPermissions, results...)
	}

	if len(missingPermissions) > 0 {
		jsonBytes, err := json.Marshal(missingPermissions)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify data plane identities permissions.",
				fmt.Sprintf("failed to marshal missing permissions: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		internalAndUserMsg := fmt.Sprintf("Data plane operators missing required permissions: %s", string(jsonBytes))
		return FailedValidation(
			"MissingRequiredPermissions",
			internalAndUserMsg,
			internalAndUserMsg,
		)
	}

	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		"Data plane operators have all required permissions.",
		"Data plane operators have all required permissions.",
	)
}

func (v *DataPlaneIdentitiesPermissionsValidation) roleActionsForOperator(ctx context.Context, operatorName string) ([]string, error) {
	operatorIdentity, ok := v.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("operator identity %q not found in data plane identities config", operatorName))
	}
	roleDefinitionsResourceIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for operator identity %q", operatorName))
	}
	roleDefinitions, err := v.fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs)
	if err != nil {
		return nil, err
	}
	return azurehelpers.UnionActions(roleDefinitions)
}

func (v *DataPlaneIdentitiesPermissionsValidation) roleDataActionsForOperator(ctx context.Context, operatorName string) ([]string, error) {
	operatorIdentity, ok := v.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("operator identity %q not found in data plane identities config", operatorName))
	}
	roleDefinitionsResourceIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, nil
	}
	roleDefinitions, err := v.fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs)
	if err != nil {
		return nil, err
	}
	return azurehelpers.UnionDataActions(roleDefinitions)
}

// fetchRoleDefinitions fetches the role definitions for the given resource IDs.
func (v *DataPlaneIdentitiesPermissionsValidation) fetchRoleDefinitions(ctx context.Context, resourceIDs []*azcorearm.ResourceID) ([]armauthorization.RoleDefinition, error) {
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

// retrieveIdentityObjectID resolves the Entra object ID (PrincipalID) of the given data plane operator identity. This is the CheckAccessV2 subject for data plane operators: unlike control plane operators,
// MI Dataplane cannot mint an access token for them, so CheckAccessV2 must identify the subject by ObjectId instead of by JWT. See createAuthorizationRequestForDataPlaneIdentity.
func (v *DataPlaneIdentitiesPermissionsValidation) retrieveIdentityObjectID(ctx context.Context, userAssignedIdentitiesClient azureclient.UserAssignedIdentitiesClient, identity *azcorearm.ResourceID) (string, error) {
	operatorIdentity, err := userAssignedIdentitiesClient.Get(ctx, identity.ResourceGroupName, identity.Name, nil)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get user assigned managed identity %q: %w", identity, err))
	}
	if operatorIdentity.Properties == nil {
		return "", utils.TrackError(fmt.Errorf("properties are nil for user assigned managed identity %q", identity))
	}
	if operatorIdentity.Properties.PrincipalID == nil {
		return "", utils.TrackError(fmt.Errorf("principal ID is nil for user assigned managed identity %q", identity))
	}
	return *operatorIdentity.Properties.PrincipalID, nil
}

func (v *DataPlaneIdentitiesPermissionsValidation) findMissingActionsForIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, identityObjectID string, cluster *coreapi.HCPOpenShiftCluster, operatorName string, identity *azcorearm.ResourceID, clusterSubnet *armnetwork.Subnet) ([]*identityResourceMissingPermissions, error) {
	roleActions, err := v.roleActionsForOperator(ctx, operatorName)
	if err != nil {
		return nil, err
	}

	roleDataActions, err := v.roleDataActionsForOperator(ctx, operatorName)
	if err != nil {
		return nil, err
	}

	var results []*identityResourceMissingPermissions

	nsgResult, err := v.checkMissingPermissionsForNetworkSecurityGroup(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.NetworkSecurityGroupID, identity, identityObjectID, roleActions, roleDataActions)
	if err != nil {
		return nil, err
	}
	if nsgResult != nil {
		results = append(results, nsgResult)
	}

	vnetResult, err := v.checkMissingPermissionsForVNet(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.SubnetID, identity, identityObjectID, roleActions, roleDataActions)
	if err != nil {
		return nil, err
	}
	if vnetResult != nil {
		results = append(results, vnetResult)
	}

	subnetResult, err := v.checkMissingPermissionsForSubnet(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.SubnetID, identity, identityObjectID, roleActions, roleDataActions)
	if err != nil {
		return nil, err
	}
	if subnetResult != nil {
		results = append(results, subnetResult)
	}

	natGatewayResult, err := v.checkMissingPermissionsForNatGateway(ctx, checkAccessV2Client, clusterSubnet, identity, identityObjectID, roleActions, roleDataActions)
	if err != nil {
		return nil, err
	}
	if natGatewayResult != nil {
		results = append(results, natGatewayResult)
	}

	rtResult, err := v.checkMissingPermissionsForRouteTable(ctx, checkAccessV2Client, clusterSubnet, identity, identityObjectID, roleActions, roleDataActions)
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
//   - a non-nil *identityResourceMissingPermissions populated with the NSG resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *DataPlaneIdentitiesPermissionsValidation) checkMissingPermissionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, nsgID *azcorearm.ResourceID, identity *azcorearm.ResourceID, identityObjectID string, roleActions []string, roleDataActions []string) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgID, roleActions, roleDataActions, identityObjectID)
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
//   - a non-nil *identityResourceMissingPermissions populated with the VNet resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *DataPlaneIdentitiesPermissionsValidation) checkMissingPermissionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnetID *azcorearm.ResourceID, identity *azcorearm.ResourceID, identityObjectID string, roleActions []string, roleDataActions []string) (*identityResourceMissingPermissions, error) {
	vnetResourceID := subnetID.Parent
	decisions, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetResourceID, roleActions, roleDataActions, identityObjectID)
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

// checkMissingPermissionsForSubnet checks whether the given identity has all required permissions on the cluster subnet itself.
// Only actions from roleActions/roleDataActions that are relevant to subnet resources are checked (via intersection with the known subnet action set); actions irrelevant to subnets are skipped.
// It returns:
//   - (nil, nil) if the identity has all required permissions, or if none of the role's actions apply to subnet resources.
//   - a non-nil *identityResourceMissingPermissions populated with the subnet resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *DataPlaneIdentitiesPermissionsValidation) checkMissingPermissionsForSubnet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnetID *azcorearm.ResourceID, identity *azcorearm.ResourceID, identityObjectID string, roleActions []string, roleDataActions []string) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForSubnet(ctx, checkAccessV2Client, subnetID, roleActions, roleDataActions, identityObjectID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  subnetID,
		Identity:  identity,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForNatGateway checks whether the given identity has all required permissions on the NAT gateway attached to the cluster subnet. If the subnet has no NAT gateway, it is a no-op.
// Only actions from roleActions/roleDataActions that are relevant to NAT gateway resources are checked (via intersection with the known NAT gateway action set).
// It returns:
//   - (nil, nil) if the subnet has no attached NAT gateway (or the attached NAT gateway has no resource ID), if the identity has all required permissions, or if none of the role's actions apply to NAT gateway resources.
//   - a non-nil *identityResourceMissingPermissions populated with the NAT gateway resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the subnet properties are unexpectedly absent, the NAT gateway resource ID cannot be parsed, or the CheckAccessV2 API call fails.
func (v *DataPlaneIdentitiesPermissionsValidation) checkMissingPermissionsForNatGateway(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, clusterSubnet *armnetwork.Subnet, identity *azcorearm.ResourceID, identityObjectID string, roleActions []string, roleDataActions []string) (*identityResourceMissingPermissions, error) {
	if clusterSubnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet properties are nil"))
	}
	if clusterSubnet.Properties.NatGateway == nil || clusterSubnet.Properties.NatGateway.ID == nil {
		return nil, nil
	}
	natGatewayResourceID, err := azcorearm.ParseResourceID(*clusterSubnet.Properties.NatGateway.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse NAT gateway resource ID: %w", err))
	}
	decisions, err := v.checkNotAllowedAndDeniedActionsForNatGateway(ctx, checkAccessV2Client, clusterSubnet.Properties.NatGateway, roleActions, roleDataActions, identityObjectID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  natGatewayResourceID,
		Identity:  identity,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForRouteTable checks whether the given identity has all required permissions on the route table attached to the cluster subnet. If the subnet has no route table, it is a no-op.
// Only actions from roleActions/roleDataActions that are relevant to route table resources are checked (via intersection with the known route table action set).
// It returns:
//   - (nil, nil) if the subnet has no attached route table (or the attached route table has no resource ID), if the identity has all required permissions, or if none of the role's actions apply to route table resources.
//   - a non-nil *identityResourceMissingPermissions populated with the route table resource ID, the identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the subnet properties are unexpectedly absent, the route table resource ID cannot be parsed, or the CheckAccessV2 API call fails.
func (v *DataPlaneIdentitiesPermissionsValidation) checkMissingPermissionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, clusterSubnet *armnetwork.Subnet, identity *azcorearm.ResourceID, identityObjectID string, roleActions []string, roleDataActions []string) (*identityResourceMissingPermissions, error) {
	if clusterSubnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet properties are nil"))
	}
	if clusterSubnet.Properties.RouteTable == nil || clusterSubnet.Properties.RouteTable.ID == nil {
		return nil, nil
	}
	routeTableResourceID, err := azcorearm.ParseResourceID(*clusterSubnet.Properties.RouteTable.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
	}
	decisions, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client, clusterSubnet.Properties.RouteTable, roleActions, roleDataActions, identityObjectID)
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

// checkNotAllowedAndDeniedActionsForNetworkSecurityGroup checks whether the identity represented by identityObjectID has all required permissions on the given network security group resource.
// Only actions from roleDefinitionActions/roleDefinitionDataActions that are relevant to NSG resources are checked (via intersection with the known NSG action set); unrelated actions are
// ignored. This avoids sending actions to CheckAccessV2 that a given operator role was never expected to hold on an NSG.
// It returns:
//   - (nil, nil) if the identity has all required permissions, or if none of the role's actions apply to NSG resources.
//   - a non-nil slice of NotAllowed/Denied AuthorizationDecision entries, one per missing action, if any permission is absent.
//   - (nil, error) if the CheckAccessV2 API call fails.
func (v *DataPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, identityObjectID string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/networkSecurityGroups/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	networkSecurityGroupActions := []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
	var networkSecurityGroupDataActions []string

	requiredActions := azurehelpers.IntersectActions(networkSecurityGroupActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(networkSecurityGroupDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, requiredActions, requiredDataActions, identityObjectID)
}

// checkNotAllowedAndDeniedActionsForResourceID checks whether the identity represented by identityObjectID has permission to perform the specified `actions` and `dataActions` on the given `resourceID` using the
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
func (v *DataPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForResourceID(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, actions []string, dataActions []string, identityObjectID string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	totalExpected := len(actions) + len(dataActions)
	if totalExpected == 0 {
		return nil, nil
	}

	authRequest := v.createAuthorizationRequestForDataPlaneIdentity(identityObjectID, resourceID, actions, dataActions)

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

	notAllowedAndDeniedActions := collectNotAllowedAndDeniedActions(authDecisionResponse.Value)
	return notAllowedAndDeniedActions, nil
}

func (v *DataPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, identityObjectID string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/virtualNetworks/* actions (excluding subnet actions, which are checked separately) that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	virtualNetworkActions := []string{
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/join/action",
	}
	var virtualNetworkDataActions []string

	requiredActions := azurehelpers.IntersectActions(virtualNetworkActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(virtualNetworkDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, requiredActions, requiredDataActions, identityObjectID)
}

func (v *DataPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForSubnet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, identityObjectID string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/virtualNetworks/subnets/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	subnetActions := []string{
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
	}
	var subnetDataActions []string

	requiredActions := azurehelpers.IntersectActions(subnetActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(subnetDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, requiredActions, requiredDataActions, identityObjectID)
}

func (v *DataPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForNatGateway(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, natGateway *armnetwork.SubResource, roleDefinitionActions []string, roleDefinitionDataActions []string, identityObjectID string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/natGateways/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	natGatewayActions := []string{
		"Microsoft.Network/natGateways/read",
		"Microsoft.Network/natGateways/write",
		"Microsoft.Network/natGateways/join/action",
	}
	var natGatewayDataActions []string

	requiredActions := azurehelpers.IntersectActions(natGatewayActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(natGatewayDataActions, roleDefinitionDataActions)

	natGatewayResourceID, err := azcorearm.ParseResourceID(*natGateway.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse NAT gateway resource ID: %w", err))
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, natGatewayResourceID, requiredActions, requiredDataActions, identityObjectID)
}

func (v *DataPlaneIdentitiesPermissionsValidation) checkNotAllowedAndDeniedActionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, routeTable *armnetwork.RouteTable, roleDefinitionActions []string, roleDefinitionDataActions []string, identityObjectID string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/routeTables/* actions that appear across any operator role in internal/azure/cluster_scoped_identities_config.go
	routeTableActions := []string{
		"Microsoft.Network/routeTables/read",
		"Microsoft.Network/routeTables/write",
		"Microsoft.Network/routeTables/join/action",
	}
	var routeTableDataActions []string

	requiredActions := azurehelpers.IntersectActions(routeTableActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(routeTableDataActions, roleDefinitionDataActions)

	routeTableResourceID, err := azcorearm.ParseResourceID(*routeTable.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, routeTableResourceID, requiredActions, requiredDataActions, identityObjectID)
}

// createAuthorizationRequestForDataPlaneIdentity builds a CheckAccessV2 AuthorizationRequest that identifies the subject by Entra object ID rather than by JWT. Note that SubjectAttributes.Groups is
// intentionally left unset here: unlike the JWT-based path, there is no token to source group claims from, so role assignments granted to a group the identity belongs to (rather than to the identity
// directly) are not visible to this check.
//
// Unlike createAuthorizationRequestForControlPlaneIdentity, this cannot delegate to CheckAccessV2Client.CreateAuthorizationRequest, since that helper builds its request from a JWT and data plane
// operators have no token to pass. The AuthorizationRequest is therefore constructed manually here, populating Subject.Attributes.ObjectId directly instead.
func (v *DataPlaneIdentitiesPermissionsValidation) createAuthorizationRequestForDataPlaneIdentity(subject string, resourceID *azcorearm.ResourceID,
	actions []string, dataActions []string) *azurecheckaccessv2client.AuthorizationRequest {
	actionInfos := []azurecheckaccessv2client.ActionInfo{}
	for _, action := range actions {
		actionInfos = append(actionInfos, azurecheckaccessv2client.ActionInfo{Id: action})
	}
	for _, da := range dataActions {
		actionInfos = append(actionInfos, azurecheckaccessv2client.ActionInfo{
			Id:           da,
			IsDataAction: true,
		})
	}
	return &azurecheckaccessv2client.AuthorizationRequest{
		Subject: azurecheckaccessv2client.SubjectInfo{
			Attributes: azurecheckaccessv2client.SubjectAttributes{
				ObjectId: subject,
			},
		},
		Actions: actionInfos,
		Resource: azurecheckaccessv2client.ResourceInfo{
			Id: resourceID.String(),
		},
	}
}
