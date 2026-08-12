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
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azurehelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DataPlaneIdentitiesPermissionsValidation validates that the data plane identities have the necessary permissions.
//
// Unlike ControlPlaneIdentitiesPermissionsClusterValidation, which mints a JWT for each control-plane operator identity via MI Dataplane and passes that token to CheckAccessV2, this validator identifies
// each data-plane operator to CheckAccessV2 by its Entra object ID (PrincipalID from the UAI). MI Dataplane cannot mint access tokens for data-plane operator identities, so the ObjectId subject
// path is required. See createAuthorizationRequestForDataPlaneIdentity.
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
	uaisClient, err := v.smiClientBuilder.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
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

	// Build the list of resources to validate permissions for.
	resourceChecks, err := v.buildResourceChecks(ctx, cluster, subnetsClient)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify data plane identities permissions.",
			fmt.Sprintf("failed to build resource checks: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	var missingPermissions []*identityResourceMissingPermissions
	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		operatorIdentity, err := uaisClient.Get(ctx, identity.ResourceGroupName, identity.Name, nil)

		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify data plane identities permissions.",
				fmt.Sprintf("failed to get user assigned managed identity: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}

		if operatorIdentity.Properties == nil || operatorIdentity.Properties.PrincipalID == nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify data plane identities permissions.",
				fmt.Sprintf("principal ID is nil for operator %s", operatorName),
				ControllerReportingPolicyTypeError,
			)
		}

		// PrincipalID is the Entra object ID used as the CheckAccessV2 subject. We cannot mint a JWT for this identity via MI Dataplane (unlike control-plane operators)
		identityObjectID := *operatorIdentity.Properties.PrincipalID
		results, err := v.findMissingActionsForIdentity(ctx, checkAccessV2Client, operatorName, identity, identityObjectID, resourceChecks)
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

func (v *DataPlaneIdentitiesPermissionsValidation) findMissingActionsForIdentity(
	ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, operatorName string, identity *azcorearm.ResourceID, identityObjectID string, resourceChecks []resourcePermissionCheck) ([]*identityResourceMissingPermissions, error) {

	roleActions, err := v.roleActionsForOperator(ctx, operatorName)
	if err != nil {
		return nil, err
	}

	roleDataActions, err := v.roleDataActionsForOperator(ctx, operatorName)
	if err != nil {
		return nil, err
	}

	var results []*identityResourceMissingPermissions

	for _, check := range resourceChecks {
		result, err := v.checkMissingPermissionsOnResource(ctx, checkAccessV2Client, check, identityObjectID, roleActions, roleDataActions)
		if err != nil {
			return nil, err
		}
		if len(result) > 0 {
			results = append(results, &identityResourceMissingPermissions{
				Resource:  check.resourceID,
				Identity:  identity,
				Decisions: result,
			})
		}
	}

	return results, nil
}

func (v *DataPlaneIdentitiesPermissionsValidation) checkMissingPermissionsOnResource(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, check resourcePermissionCheck, identityObjectID string, roleActions []string, roleDataActions []string) ([]*checkaccessv2AuthorizationDecisionData, error) {
	requiredActions := azurehelpers.IntersectActions(check.requiredActions, roleActions)
	requiredDataActions := azurehelpers.IntersectActions(check.requiredDataActions, roleDataActions)
	if len(requiredActions) == 0 && len(requiredDataActions) == 0 {
		return nil, nil
	}

	totalExpected := len(requiredActions) + len(requiredDataActions)

	authReq := v.createAuthorizationRequestForDataPlaneIdentity(identityObjectID, check.resourceID, requiredActions, requiredDataActions)
	authDecisionResponse, err := checkAccessV2Client.CheckAccess(ctx, *authReq)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if authDecisionResponse == nil {
		return nil, utils.TrackError(fmt.Errorf("authorization response for %s '%s' is unexpectedly nil, indicating a possible internal failure", check.resourceType, check.resourceID.String()))
	}
	if totalExpected != len(authDecisionResponse.Value) {
		err := fmt.Errorf("mismatch in authorization decision response for %s '%s': expected '%d' actions, got '%d' actions", check.resourceType, check.resourceID.String(), totalExpected, len(authDecisionResponse.Value))
		return nil, utils.TrackError(err)
	}

	notAllowedAndDeniedActions := collectNotAllowedAndDeniedActions(authDecisionResponse.Value)

	return notAllowedAndDeniedActions, nil
}

// createAuthorizationRequestForDataPlaneIdentity builds a CheckAccessV2 AuthorizationRequest that identifies the subject by Entra object ID rather than by JWT.
func (v *DataPlaneIdentitiesPermissionsValidation) createAuthorizationRequestForDataPlaneIdentity(subject string, resourceId *azcorearm.ResourceID,
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
			Id: resourceId.String(),
		},
	}
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
	roleDefinitions, err := fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs, v.backendIdentityAzureCachedReaders)
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
	roleDefinitions, err := fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs, v.backendIdentityAzureCachedReaders)
	if err != nil {
		return nil, err
	}
	return azurehelpers.UnionDataActions(roleDefinitions)
}

type resourcePermissionCheck struct {
	resourceID          *azcorearm.ResourceID
	resourceType        string // For logging purposes
	requiredActions     []string
	requiredDataActions []string
}

const (
	resourceTypeNetworkSecurityGroup = "NetworkSecurityGroup"
	resourceTypeVirtualNetwork       = "VirtualNetwork"
	resourceTypeSubnet               = "Subnet"
	resourceTypeNatGateway           = "NatGateway"
	resourceTypeRouteTable           = "RouteTable"
)

// Azure resource action definitions for permission validation.
var (
	networkSecurityGroupActions = []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
	virtualNetworkActions = []string{
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/join/action",
	}
	subnetActions = []string{
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
	}
	natGatewayActions = []string{
		"Microsoft.Network/natGateways/read",
		"Microsoft.Network/natGateways/write",
		"Microsoft.Network/natGateways/join/action",
	}
	routeTableActions = []string{
		"Microsoft.Network/routeTables/read",
		"Microsoft.Network/routeTables/write",
		"Microsoft.Network/routeTables/join/action",
	}
)

// subnetAttachedResources holds resource IDs of resources attached to a subnet
type subnetAttachedResources struct {
	natGatewayResourceID *azcorearm.ResourceID
	routeTableResourceID *azcorearm.ResourceID
}

// buildResourceChecks constructs the list of resources to validate permissions for.
func (v *DataPlaneIdentitiesPermissionsValidation) buildResourceChecks(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, subnetsClient azureclient.SubnetsClient) ([]resourcePermissionCheck, error) {
	subnetResourceID := cluster.CustomerProperties.Platform.SubnetID
	virtualNetworkResourceID := subnetResourceID.Parent
	networkSecurityGroupResourceID := cluster.CustomerProperties.Platform.NetworkSecurityGroupID

	var checks []resourcePermissionCheck

	// Add network security group
	checks = append(checks, resourcePermissionCheck{
		resourceID:      networkSecurityGroupResourceID,
		resourceType:    resourceTypeNetworkSecurityGroup,
		requiredActions: networkSecurityGroupActions,
	})

	// Add virtual network
	checks = append(checks, resourcePermissionCheck{
		resourceID:      virtualNetworkResourceID,
		resourceType:    resourceTypeVirtualNetwork,
		requiredActions: virtualNetworkActions,
	})

	// Add subnet
	checks = append(checks, resourcePermissionCheck{
		resourceID:      subnetResourceID,
		resourceType:    resourceTypeSubnet,
		requiredActions: subnetActions,
	})

	// Add subnet-attached resources (NAT Gateway, Route Table)
	attachedResources, err := v.getSubnetAttachedResources(ctx, subnetResourceID, subnetsClient)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get attached resources for subnet '%s': %w", subnetResourceID, err))
	}

	if attachedResources.natGatewayResourceID != nil {
		checks = append(checks, resourcePermissionCheck{
			resourceID:      attachedResources.natGatewayResourceID,
			resourceType:    resourceTypeNatGateway,
			requiredActions: natGatewayActions,
		})
	}
	if attachedResources.routeTableResourceID != nil {
		checks = append(checks, resourcePermissionCheck{
			resourceID:      attachedResources.routeTableResourceID,
			resourceType:    resourceTypeRouteTable,
			requiredActions: routeTableActions,
		})
	}

	return checks, nil
}

// getSubnetAttachedResources fetches the subnet details and returns the attached resource IDs (NAT Gateway, Route Table) if they are attached to the subnet.
func (v *DataPlaneIdentitiesPermissionsValidation) getSubnetAttachedResources(ctx context.Context, subnetResourceId *azcorearm.ResourceID, subnetsClient azureclient.SubnetsClient) (subnetAttachedResources, error) {
	result := subnetAttachedResources{}

	subnetResp, err := subnetsClient.Get(ctx, subnetResourceId.ResourceGroupName, subnetResourceId.Parent.Name, subnetResourceId.Name, nil)
	if err != nil {
		return result, utils.TrackError(fmt.Errorf("failed to get subnet '%s': %w", subnetResourceId, err))
	}

	if subnetResp.Properties != nil {
		if subnetResp.Properties.NatGateway != nil && subnetResp.Properties.NatGateway.ID != nil {
			natGatewayResourceID, err := azcorearm.ParseResourceID(*subnetResp.Properties.NatGateway.ID)
			if err != nil {
				return result, utils.TrackError(fmt.Errorf("failed to parse NAT gateway resource ID: %w", err))
			}
			result.natGatewayResourceID = natGatewayResourceID
		}
		if subnetResp.Properties.RouteTable != nil && subnetResp.Properties.RouteTable.ID != nil {
			routeTableResourceID, err := azcorearm.ParseResourceID(*subnetResp.Properties.RouteTable.ID)
			if err != nil {
				return result, utils.TrackError(fmt.Errorf("failed to parse route table resource ID: %w", err))
			}
			result.routeTableResourceID = routeTableResourceID
		}
	}

	return result, nil
}
