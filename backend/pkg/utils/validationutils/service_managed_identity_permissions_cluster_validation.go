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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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

// ServiceManagedIdentityPermissionsClusterValidation validates that the cluster's service managed identity (SMI) has the RBAC permissions it needs
// over the expected resources.
type ServiceManagedIdentityPermissionsClusterValidation struct {
	smiClientBuilder                            azureclient.ServiceManagedIdentityClientBuilder
	clusterScopedIdentitiesConfig               *azure.ClusterScopedIdentitiesConfig
	backendIdentityAzureCachedReaders           *cachedreader.BackendIdentityAzureCachedReaders
	checkAccessV2ClientBuilder                  azureclient.CheckAccessV2ClientBuilder
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	// checkAccessV2Scope is the OAuth scope (typically a "<resource>/.default" App ID URI. public, gov, and China clouds
	// each use a different App ID URI respectively) passed to MI Dataplane when minting an access token for the service managed identity.
	checkAccessV2Scope string
}

func NewServiceManagedIdentityPermissionsClusterValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	backendIdentityAzureCachedReaders *cachedreader.BackendIdentityAzureCachedReaders,
	checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder,
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
	checkAccessV2Scope string,
) *ServiceManagedIdentityPermissionsClusterValidation {
	return &ServiceManagedIdentityPermissionsClusterValidation{
		smiClientBuilder:                            smiClientBuilder,
		clusterScopedIdentitiesConfig:               clusterScopedIdentitiesConfig,
		backendIdentityAzureCachedReaders:           backendIdentityAzureCachedReaders,
		checkAccessV2ClientBuilder:                  checkAccessV2ClientBuilder,
		miDataplaneBasedAccessTokenRetrieverBuilder: miDataplaneBasedAccessTokenRetrieverBuilder,
		checkAccessV2Scope:                          checkAccessV2Scope,
	}
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) Name() string {
	return "ServiceManagedIdentityPermissionsClusterValidation"
}

var _ ClusterValidation = (*ServiceManagedIdentityPermissionsClusterValidation)(nil)

func (v *ServiceManagedIdentityPermissionsClusterValidation) Validate(ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.Cluster) ValidationResult {
	checkAccessV2Client, err := v.checkAccessV2ClientBuilder.Build(*clusterSubscription.Properties.TenantId)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("failed to build check access client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity

	roleActions, err := v.roleActionsForServiceManagedIdentity(ctx)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("failed to fetch cluster's service managed identity role actions: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	roleDataActions, err := v.roleDataActionsForServiceManagedIdentity(ctx)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("failed to fetch cluster's service managed identity role data actions: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	token, err := v.accessTokenForServiceManagedIdentity(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("failed to get access token for cluster's service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	subnetsClient, err := v.smiClientBuilder.SubnetsClient(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("failed to get subnets client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	// Fetch the cluster's subnet details to discover subnet-attached resources (NAT gateway, route table, NSG) to validate permissions
	// over. This Get is itself performed as the service managed identity, so if the SMI genuinely lacks subnet read
	// permission, it fails here too. That must not abort the rest of the validation (the CheckAccessV2-based checks below
	// don't depend on it, and the subnet's own "read" action is already covered by checkMissingPermissionsForSubnet). Such
	// a Get error is instead collected and only surfaces as an Unknown outcome if nothing else was already conclusively
	// found missing.
	// clusterSubnetFetchErr and vnetIntegrationSubnetFetchErr record why their respective subnet's Get failed, if it did.
	// The raw error is for logs/diagnostics only. Only the already-known, safe-to-surface resource ID
	// (subnetResourceID / vnetIntegrationSubnetID) is used when building the user-facing messages below.
	var clusterSubnet *armnetwork.Subnet
	var clusterSubnetFetchErr error
	subnetResourceID := cluster.CustomerProperties.Platform.SubnetID
	subnetResponse, err := subnetsClient.Get(ctx, subnetResourceID.ResourceGroupName, subnetResourceID.Parent.Name, subnetResourceID.Name, nil)
	switch {
	case err != nil:
		clusterSubnetFetchErr = err
	case subnetResponse.Properties == nil:
		// We don't expect Azure to return a subnet with no properties, so this is an unexpected failure mode and we
		// short-circuit the validation if that occurs.
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("cluster's subnet %q was returned with no properties", subnetResourceID.String()),
			ControllerReportingPolicyTypeError,
		)
	default:
		clusterSubnet = &subnetResponse.Subnet
	}

	// The VNet integration subnet is only present on clusters with VNet integration subnet configured. Fetch its details too so its attached
	// resources (NAT gateway, route table, NSG) can be validated the same way as the cluster's main subnet.
	var vnetIntegrationSubnet *armnetwork.Subnet
	var vnetIntegrationSubnetFetchErr error
	vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID
	if vnetIntegrationSubnetID != nil {
		vnetIntegrationSubnetResponse, err := subnetsClient.Get(ctx, vnetIntegrationSubnetID.ResourceGroupName, vnetIntegrationSubnetID.Parent.Name, vnetIntegrationSubnetID.Name, nil)
		switch {
		case err != nil:
			vnetIntegrationSubnetFetchErr = err
		case vnetIntegrationSubnetResponse.Properties == nil:
			// We don't expect Azure to return a subnet with no properties, so this is an unexpected failure mode and we
			// short-circuit the validation if that occurs.
			return UnknownValidation(
				"InternalError",
				"Unable to verify cluster's service managed identity permissions.",
				fmt.Sprintf("cluster's vnet integration subnet %q was returned with no properties", vnetIntegrationSubnetID.String()),
				ControllerReportingPolicyTypeError,
			)
		default:
			vnetIntegrationSubnet = &vnetIntegrationSubnetResponse.Subnet
		}
	}

	missingPermissions, err := v.findMissingActionsForServiceManagedIdentity(
		ctx, checkAccessV2Client, cluster, smiResourceID,
		roleActions, roleDataActions,
		token, clusterSubnet, vnetIntegrationSubnet,
	)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify cluster's service managed identity permissions.",
			fmt.Sprintf("failed to find missing actions: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	if len(missingPermissions) > 0 {
		jsonBytes, err := json.Marshal(missingPermissions)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify cluster's service managed identity permissions.",
				fmt.Sprintf("failed to marshal missing permissions: %v", err),
				ControllerReportingPolicyTypeError,
			)
		}
		userMsg := fmt.Sprintf("Cluster's service managed identity missing required permissions over expected resources: %s", string(jsonBytes))
		internalMsg := userMsg
		if subnetFetchUserMsg, subnetFetchInternalMsg := subnetFetchFailureMessage(subnetResourceID, clusterSubnetFetchErr, vnetIntegrationSubnetID, vnetIntegrationSubnetFetchErr); subnetFetchUserMsg != "" {
			userMsg += " Additionally, " + subnetFetchUserMsg + "."
			internalMsg += fmt.Sprintf(" (additionally, some checks could not be completed: %s)", subnetFetchInternalMsg)
		}
		return FailedValidation(
			"MissingRequiredPermissions",
			userMsg,
			internalMsg,
		)
	}

	if subnetFetchUserMsg, subnetFetchInternalMsg := subnetFetchFailureMessage(subnetResourceID, clusterSubnetFetchErr, vnetIntegrationSubnetID, vnetIntegrationSubnetFetchErr); subnetFetchUserMsg != "" {
		return UnknownValidation(
			"InternalError",
			"Unable to fully verify cluster's service managed identity permissions: "+subnetFetchUserMsg+".",
			fmt.Sprintf("no missing permissions were found among the checks that could be completed, but some checks could not be completed: %s", subnetFetchInternalMsg),
			ControllerReportingPolicyTypeError,
		)
	}

	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		"Cluster's service managed identity has all required permissions over expected resources.",
		"Cluster's service managed identity has all required permissions over expected resources.",
	)
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) roleActionsForServiceManagedIdentity(ctx context.Context) ([]string, error) {
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ServiceManagedIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionsResourceIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for the service managed identity"))
	}
	roleDefinitions, err := v.fetchRoleDefinitions(ctx, roleDefinitionsResourceIDs)
	if err != nil {
		return nil, err
	}
	return azurehelpers.UnionActions(roleDefinitions)
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) roleDataActionsForServiceManagedIdentity(ctx context.Context) ([]string, error) {
	roleDefinitionsResourceIDs := v.clusterScopedIdentitiesConfig.ServiceManagedIdentity.RoleDefinitionsResourceIDs()
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
func (v *ServiceManagedIdentityPermissionsClusterValidation) fetchRoleDefinitions(ctx context.Context, resourceIDs []*azcorearm.ResourceID) ([]armauthorization.RoleDefinition, error) {
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

// subnetFetchFailureMessage builds a user-facing message (naming only the affected subnet resource IDs - the customer's
// own resources, not sensitive) and an internal-only message (which additionally includes the raw fetch error, for
// logs/diagnostics only - see ValidationResult.InternalMessage) for whichever of the two subnet fetches failed.
// Returns ("", "") if neither failed.
func subnetFetchFailureMessage(subnetResourceID *azcorearm.ResourceID, clusterSubnetFetchErr error, vnetIntegrationSubnetID *azcorearm.ResourceID, vnetIntegrationSubnetFetchErr error) (userMsg string, internalMsg string) {
	var userParts, internalParts []string
	if clusterSubnetFetchErr != nil {
		userParts = append(userParts, subnetResourceID.String())
		internalParts = append(internalParts, fmt.Sprintf("failed to get subnet %s: %v", subnetResourceID, clusterSubnetFetchErr))
	}
	if vnetIntegrationSubnetFetchErr != nil {
		userParts = append(userParts, vnetIntegrationSubnetID.String())
		internalParts = append(internalParts, fmt.Sprintf("failed to get vnet integration subnet %s: %v", vnetIntegrationSubnetID, vnetIntegrationSubnetFetchErr))
	}
	if len(userParts) == 0 {
		return "", ""
	}
	return fmt.Sprintf("permissions over subnet-attached resources (NAT gateway, route table, NSG) could not be fully verified for: %s", strings.Join(userParts, ", ")),
		strings.Join(internalParts, "; ")
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) accessTokenForServiceManagedIdentity(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID) (azcore.AccessToken, error) {
	retriever, err := v.miDataplaneBasedAccessTokenRetrieverBuilder.Build(clusterIdentityURL, smiResourceID)
	if err != nil {
		return azcore.AccessToken{}, utils.TrackError(err)
	}
	token, err := retriever.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{v.checkAccessV2Scope}})
	if err != nil {
		return azcore.AccessToken{}, utils.TrackError(err)
	}
	return token, nil
}

// findMissingActionsForServiceManagedIdentity checks the service managed identity's permissions over the cluster's network resources (NSG, VNet, subnet, and
// the NAT gateway/route table/NSG attached to the cluster's subnet and, if the cluster is SWIFT-networked, to its VNet integration subnet), over each data
// plane operator's managed identity, and over each control plane operator's managed identity, returning the aggregate list of resources on which a
// permission is missing.
//
// roleActions/roleDataActions are the service managed identity's managed-resource-group-scoped role actions, used for the network resource checks.
// The control plane and data plane operator identity checks use a hardcoded set of required actions instead, since those permissions are
// required for every service managed identity regardless of which roles are configured for it.
//
// clusterSubnet and vnetIntegrationSubnet may be nil when the caller was unable to fetch the corresponding subnet (e.g. the service managed identity
// itself lacks subnet read permission); the attached-resource checks that depend on that subnet data are then skipped rather than failing the whole check.
func (v *ServiceManagedIdentityPermissionsClusterValidation) findMissingActionsForServiceManagedIdentity(
	ctx context.Context,
	checkAccessV2Client azureclient.CheckAccessV2Client,
	cluster *coreapi.Cluster,
	smiResourceID *azcorearm.ResourceID,
	roleActions []string,
	roleDataActions []string,
	token azcore.AccessToken,
	clusterSubnet *armnetwork.Subnet,
	vnetIntegrationSubnet *armnetwork.Subnet,
) ([]*identityResourceMissingPermissions, error) {
	var results []*identityResourceMissingPermissions

	nsgResult, err := v.checkMissingPermissionsForNetworkSecurityGroup(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.NetworkSecurityGroupID, smiResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if nsgResult != nil {
		results = append(results, nsgResult)
	}

	vnetResult, err := v.checkMissingPermissionsForVNet(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.SubnetID.Parent, smiResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if vnetResult != nil {
		results = append(results, vnetResult)
	}

	subnetResult, err := v.checkMissingPermissionsForSubnet(ctx, checkAccessV2Client, cluster.CustomerProperties.Platform.SubnetID, smiResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if subnetResult != nil {
		results = append(results, subnetResult)
	}

	if clusterSubnet != nil {
		clusterSubnetAttachedResults, err := v.checkMissingPermissionsForSubnetAttachedResources(ctx, checkAccessV2Client, clusterSubnet, smiResourceID, roleActions, roleDataActions, token)
		if err != nil {
			return nil, err
		}
		results = append(results, clusterSubnetAttachedResults...)
	}

	if vnetIntegrationSubnetID := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID; vnetIntegrationSubnetID != nil {
		vnetIntegrationVNetResult, err := v.checkMissingPermissionsForVNet(ctx, checkAccessV2Client, vnetIntegrationSubnetID.Parent, smiResourceID, roleActions, roleDataActions, token)
		if err != nil {
			return nil, err
		}
		if vnetIntegrationVNetResult != nil {
			results = append(results, vnetIntegrationVNetResult)
		}

		vnetIntegrationSubnetResult, err := v.checkMissingPermissionsForSubnet(ctx, checkAccessV2Client, vnetIntegrationSubnetID, smiResourceID, roleActions, roleDataActions, token)
		if err != nil {
			return nil, err
		}
		if vnetIntegrationSubnetResult != nil {
			results = append(results, vnetIntegrationSubnetResult)
		}

		if vnetIntegrationSubnet != nil {
			vnetIntegrationSubnetAttachedResults, err := v.checkMissingPermissionsForSubnetAttachedResources(ctx, checkAccessV2Client, vnetIntegrationSubnet, smiResourceID, roleActions, roleDataActions, token)
			if err != nil {
				return nil, err
			}
			results = append(results, vnetIntegrationSubnetAttachedResults...)
		}
	}

	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		result, err := v.checkMissingPermissionsForDataPlaneOperatorIdentity(ctx, checkAccessV2Client, identity, smiResourceID, token)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to find missing actions for data plane operator %q identity %s: %w", operatorName, identity, err))
		}
		if result != nil {
			results = append(results, result)
		}
	}

	for operatorName, identity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		result, err := v.checkMissingPermissionsForControlPlaneOperatorIdentity(ctx, checkAccessV2Client, identity, smiResourceID, token)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to find missing actions for control plane operator %q identity %s: %w", operatorName, identity, err))
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results, nil
}

// checkMissingPermissionsForDataPlaneOperatorIdentity checks whether the service managed identity has all required permissions over the given data plane operator managed identity resource.
// The required actions are hardcoded (see checkNotAllowedAndDeniedActionsForDataPlaneOperatorIdentity), not derived from the service managed identity's configured roles.
// It returns:
//   - (nil, nil) if the service managed identity has all required permissions.
//   - a non-nil *identityResourceMissingPermissions populated with the data plane operator identity's resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForDataPlaneOperatorIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, dataPlaneOperatorIdentity *azcorearm.ResourceID, smiResourceID *azcorearm.ResourceID, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForDataPlaneOperatorIdentity(ctx, checkAccessV2Client, dataPlaneOperatorIdentity, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  dataPlaneOperatorIdentity,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForControlPlaneOperatorIdentity checks whether the service managed identity has read permission over the given control plane
// operator's managed identity resource. Unlike data plane operators, control plane operators don't need the service managed identity to federate
// credentials for them (they authenticate via an MI Dataplane-issued JWT, not Kubernetes workload identity federation), so only read is checked.
// The required action is hardcoded (see checkNotAllowedAndDeniedActionsForControlPlaneOperatorIdentity), not derived from the service managed identity's configured roles.
// It returns:
//   - (nil, nil) if the service managed identity has read permission.
//   - a non-nil *identityResourceMissingPermissions populated with the control plane operator identity's resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if the permission is missing.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForControlPlaneOperatorIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, controlPlaneOperatorIdentity *azcorearm.ResourceID, smiResourceID *azcorearm.ResourceID, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForControlPlaneOperatorIdentity(ctx, checkAccessV2Client, controlPlaneOperatorIdentity, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  controlPlaneOperatorIdentity,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForNetworkSecurityGroup checks whether the service managed identity has all required permissions on the cluster's network security group.
// Only actions from roleActions/roleDataActions that are relevant to NSG resources are checked (via intersection with the known NSG action set); actions irrelevant to NSGs are skipped.
// It returns:
//   - (nil, nil) if the service managed identity has all required permissions, or if none of its role's actions apply to NSG resources.
//   - a non-nil *identityResourceMissingPermissions populated with the NSG resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, nsgID *azcorearm.ResourceID, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  nsgID,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForVNet checks whether the service managed identity has all required permissions on the VNet that contains the cluster's subnet.
// Only actions from roleActions/roleDataActions that are relevant to VNet resources are checked (via intersection with the known VNet action set); actions irrelevant to VNets are skipped.
// It returns:
//   - (nil, nil) if the service managed identity has all required permissions, or if none of its role's actions apply to VNet resources.
//   - a non-nil *identityResourceMissingPermissions populated with the VNet resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, vnetID *azcorearm.ResourceID, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForVNet(ctx, checkAccessV2Client, vnetID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  vnetID,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForSubnet checks whether the service managed identity has all required permissions on the cluster's subnet.
// Only actions from roleActions/roleDataActions that are relevant to subnet resources are checked (via intersection with the known subnet action set); actions irrelevant to subnets are skipped.
// It returns:
//   - (nil, nil) if the service managed identity has all required permissions, or if none of its role's actions apply to subnet resources.
//   - a non-nil *identityResourceMissingPermissions populated with the subnet resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForSubnet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnetID *azcorearm.ResourceID, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	decisions, err := v.checkNotAllowedAndDeniedActionsForSubnet(ctx, checkAccessV2Client, subnetID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  subnetID,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForSubnetAttachedResources checks whether the service managed identity has all required permissions on the NAT gateway, route table,
// and network security group attached to the given subnet. Any of the three that is not attached to the subnet is skipped (not an error).
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForSubnetAttachedResources(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnet *armnetwork.Subnet, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) ([]*identityResourceMissingPermissions, error) {
	var results []*identityResourceMissingPermissions

	natGatewayResult, err := v.checkMissingPermissionsForNatGateway(ctx, checkAccessV2Client, subnet, smiResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if natGatewayResult != nil {
		results = append(results, natGatewayResult)
	}

	rtResult, err := v.checkMissingPermissionsForRouteTable(ctx, checkAccessV2Client, subnet, smiResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if rtResult != nil {
		results = append(results, rtResult)
	}

	subnetNsgResult, err := v.checkMissingPermissionsForSubnetNetworkSecurityGroup(ctx, checkAccessV2Client, subnet, smiResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, err
	}
	if subnetNsgResult != nil {
		results = append(results, subnetNsgResult)
	}

	return results, nil
}

// checkMissingPermissionsForSubnetNetworkSecurityGroup checks whether the service managed identity has all required permissions on the network security group
// attached to the given subnet. If the subnet has no attached NSG, it is a no-op.
// It returns:
//   - (nil, nil) if the subnet has no attached NSG (or the attached NSG has no resource ID), if the service managed identity has all required permissions, or if none of its role's actions apply to NSG resources.
//   - a non-nil *identityResourceMissingPermissions populated with the NSG resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the subnet properties are unexpectedly absent, the NSG resource ID cannot be parsed, or the CheckAccessV2 API call fails.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForSubnetNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnet *armnetwork.Subnet, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
	if subnet.Properties == nil {
		return nil, utils.TrackError(fmt.Errorf("subnet properties are nil"))
	}
	if subnet.Properties.NetworkSecurityGroup == nil || subnet.Properties.NetworkSecurityGroup.ID == nil {
		return nil, nil
	}
	nsgResourceID, err := azcorearm.ParseResourceID(*subnet.Properties.NetworkSecurityGroup.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse network security group resource ID: %w", err))
	}
	decisions, err := v.checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx, checkAccessV2Client, nsgResourceID, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  nsgResourceID,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForNatGateway checks whether the service managed identity has all required permissions on the NAT gateway attached to the cluster's subnet. If the subnet has no NAT gateway, it is a no-op.
// Only actions from roleActions/roleDataActions that are relevant to NAT gateway resources are checked (via intersection with the known NAT gateway action set).
// It returns:
//   - (nil, nil) if the subnet has no attached NAT gateway (or the attached NAT gateway has no resource ID), if the service managed identity has all required permissions, or if none of its role's actions apply to NAT gateway resources.
//   - a non-nil *identityResourceMissingPermissions populated with the NAT gateway resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the subnet properties are unexpectedly absent, the NAT gateway resource ID cannot be parsed, or the CheckAccessV2 API call fails.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForNatGateway(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, clusterSubnet *armnetwork.Subnet, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
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
	decisions, err := v.checkNotAllowedAndDeniedActionsForNatGateway(ctx, checkAccessV2Client, clusterSubnet.Properties.NatGateway, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  natGatewayResourceID,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkMissingPermissionsForRouteTable checks whether the service managed identity has all required permissions on the route table attached to the cluster's subnet. If the subnet has no route table, it is a no-op.
// Only actions from roleActions/roleDataActions that are relevant to route table resources are checked (via intersection with the known route table action set).
// It returns:
//   - (nil, nil) if the subnet has no attached route table (or the attached route table has no resource ID), if the service managed identity has all required permissions, or if none of its role's actions apply to route table resources.
//   - a non-nil *identityResourceMissingPermissions populated with the route table resource ID, the service managed identity, and the slice of NotAllowed/Denied decisions, if any permission is missing.
//   - (nil, error) if the subnet properties are unexpectedly absent, the route table resource ID cannot be parsed, or the CheckAccessV2 API call fails.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkMissingPermissionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, clusterSubnet *armnetwork.Subnet, smiResourceID *azcorearm.ResourceID, roleActions []string, roleDataActions []string, token azcore.AccessToken) (*identityResourceMissingPermissions, error) {
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
	decisions, err := v.checkNotAllowedAndDeniedActionsForRouteTable(ctx, checkAccessV2Client, clusterSubnet.Properties.RouteTable, roleActions, roleDataActions, token)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(decisions) == 0 {
		return nil, nil
	}
	return &identityResourceMissingPermissions{
		Resource:  routeTableResourceID,
		Identity:  smiResourceID,
		Decisions: decisions,
	}, nil
}

// checkNotAllowedAndDeniedActionsForNetworkSecurityGroup checks whether the identity represented by the access token has all required permissions on the given network security group resource.
// Only actions from roleDefinitionActions/roleDefinitionDataActions that are relevant to NSG resources are checked (via intersection with the known NSG action set); unrelated actions are
// ignored. This avoids sending actions to CheckAccessV2 that the service managed identity's role was never expected to hold on an NSG.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForNetworkSecurityGroup(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/networkSecurityGroups/* actions that appear across any role of the service managed identity in internal/azure/cluster_scoped_identities_config.go
	networkSecurityGroupActions := []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
	var networkSecurityGroupDataActions []string

	requiredActions := azurehelpers.IntersectActions(networkSecurityGroupActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(networkSecurityGroupDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, requiredActions, requiredDataActions, token)
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForVNet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, vnetID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/virtualNetworks/* actions (excluding subnets/*) that appear across any role of the service managed identity in internal/azure/cluster_scoped_identities_config.go
	vnetActions := []string{
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/join/action",
		"Microsoft.Network/virtualNetworks/joinLoadBalancer/action",
	}
	var vnetDataActions []string

	requiredActions := azurehelpers.IntersectActions(vnetActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(vnetDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, vnetID, requiredActions, requiredDataActions, token)
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForSubnet(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, subnetID *azcorearm.ResourceID, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/virtualNetworks/subnets/* actions that appear across any role of the service managed identity in internal/azure/cluster_scoped_identities_config.go
	subnetActions := []string{
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/subnets/write",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
	}
	var subnetDataActions []string

	requiredActions := azurehelpers.IntersectActions(subnetActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(subnetDataActions, roleDefinitionDataActions)

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, subnetID, requiredActions, requiredDataActions, token)
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForNatGateway(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, natGateway *armnetwork.SubResource, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/natGateways/* actions that appear across any role of the service managed identity in internal/azure/cluster_scoped_identities_config.go
	natGatewayActions := []string{
		"Microsoft.Network/natGateways/read",
		"Microsoft.Network/natGateways/join/action",
	}
	var natGatewayDataActions []string

	requiredActions := azurehelpers.IntersectActions(natGatewayActions, roleDefinitionActions)
	requiredDataActions := azurehelpers.IntersectActions(natGatewayDataActions, roleDefinitionDataActions)

	natGatewayResourceID, err := azcorearm.ParseResourceID(*natGateway.ID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse NAT gateway resource ID: %w", err))
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, natGatewayResourceID, requiredActions, requiredDataActions, token)
}

func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForRouteTable(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, routeTable *armnetwork.RouteTable, roleDefinitionActions []string, roleDefinitionDataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// Union of all Microsoft.Network/routeTables/* actions that appear across any role of the service managed identity in internal/azure/cluster_scoped_identities_config.go
	routeTableActions := []string{
		"Microsoft.Network/routeTables/read",
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

// checkNotAllowedAndDeniedActionsForDataPlaneOperatorIdentity checks whether the service managed identity represented by the access token has all required permissions on the given user assigned managed identity resource.
// The required actions are hardcoded rather than derived from the service managed identity's configured roles, since these permissions are required
// over every data plane operator identity regardless of which roles are configured for the service managed identity.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForDataPlaneOperatorIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	// The actions the service managed identity needs over each data plane operator's managed identity, so it can federate credentials for it.
	managedIdentityActions := []string{
		"Microsoft.ManagedIdentity/userAssignedIdentities/read",
		"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read",
		"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write",
		"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete",
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, managedIdentityActions, nil, token)
}

// checkNotAllowedAndDeniedActionsForControlPlaneOperatorIdentity checks whether the service managed identity represented by the access token has read
// access on the given control plane operator's managed identity resource. Only "Microsoft.ManagedIdentity/userAssignedIdentities/read" is checked; unlike
// data plane operators, the service managed identity never needs to federate credentials for control plane operators.
//
// The required action is hardcoded rather than derived from the service managed identity's configured roles, since this permission is required
// over every control plane operator identity regardless of which roles are configured for the service managed identity.
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForControlPlaneOperatorIdentity(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	managedIdentityActions := []string{
		"Microsoft.ManagedIdentity/userAssignedIdentities/read",
	}

	return v.checkNotAllowedAndDeniedActionsForResourceID(ctx, checkAccessV2Client, resourceID, managedIdentityActions, nil, token)
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
func (v *ServiceManagedIdentityPermissionsClusterValidation) checkNotAllowedAndDeniedActionsForResourceID(ctx context.Context, checkAccessV2Client azureclient.CheckAccessV2Client, resourceID *azcorearm.ResourceID, actions []string, dataActions []string, token azcore.AccessToken) ([]*checkaccessv2AuthorizationDecisionData, error) {
	totalExpected := len(actions) + len(dataActions)
	if totalExpected == 0 {
		return nil, nil
	}

	authRequest, err := v.createAuthorizationRequestForServiceManagedIdentity(checkAccessV2Client, token, resourceID, actions, dataActions)
	if err != nil {
		return nil, utils.TrackError(err)
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

	notAllowedAndDeniedActions := collectNotAllowedAndDeniedActions(authDecisionResponse.Value)
	return notAllowedAndDeniedActions, nil
}

// createAuthorizationRequestForServiceManagedIdentity builds a CheckAccessV2 AuthorizationRequest that identifies the subject by the JWT access token minted for the service managed identity via MI Dataplane.
func (v *ServiceManagedIdentityPermissionsClusterValidation) createAuthorizationRequestForServiceManagedIdentity(checkAccessV2Client azureclient.CheckAccessV2Client, token azcore.AccessToken, resourceID *azcorearm.ResourceID,
	actions []string, dataActions []string) (*azurecheckaccessv2client.AuthorizationRequest, error) {
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
	return authRequest, nil
}
