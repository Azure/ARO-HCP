// Copyright 2025 Microsoft Corporation
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

package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	validator "github.com/go-playground/validator/v10"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftCluster represents an ARO HCP OpenShift cluster resource.
type HCPOpenShiftCluster struct {
	arm.TrackedResource
	Properties HCPOpenShiftClusterProperties `json:"properties,omitempty" validate:"required_for_put"`
	Identity   arm.ManagedServiceIdentity    `json:"identity,omitempty"`
}

// HCPOpenShiftClusterProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterProperties struct {
	ProvisioningState arm.ProvisioningState      `json:"provisioningState,omitempty"             visibility:"read"`
	Version           VersionProfile             `json:"version,omitempty"`
	DNS               DNSProfile                 `json:"dns,omitempty"`
	Network           NetworkProfile             `json:"network,omitempty"                       visibility:"read create"`
	Console           ConsoleProfile             `json:"console,omitempty"                       visibility:"read"`
	API               APIProfile                 `json:"api,omitempty"`
	Platform          PlatformProfile            `json:"platform,omitempty"                      visibility:"read create"`
	Capabilities      ClusterCapabilitiesProfile `json:"capabilities,omitempty"                  visibility:"read create"`
}

// VersionProfile represents the cluster control plane version.
type VersionProfile struct {
	ID                string   `json:"id,omitempty"                visibility:"read create"        validate:"required_unless=ChannelGroup stable,omitempty,openshift_version"`
	ChannelGroup      string   `json:"channelGroup,omitempty"      visibility:"read create update"`
	AvailableUpgrades []string `json:"availableUpgrades,omitempty" visibility:"read"`
}

// DNSProfile represents the DNS configuration of the cluster.
type DNSProfile struct {
	BaseDomain       string `json:"baseDomain,omitempty"       visibility:"read"`
	BaseDomainPrefix string `json:"baseDomainPrefix,omitempty" visibility:"read create" validate:"omitempty,dns_rfc1035_label,max=15"`
}

// NetworkProfile represents a cluster network configuration.
// Visibility for the entire struct is "read create".
type NetworkProfile struct {
	NetworkType NetworkType `json:"networkType,omitempty" validate:"omitempty,enum_networktype"`
	PodCIDR     string      `json:"podCidr,omitempty"     validate:"omitempty,cidrv4"`
	ServiceCIDR string      `json:"serviceCidr,omitempty" validate:"omitempty,cidrv4"`
	MachineCIDR string      `json:"machineCidr,omitempty" validate:"omitempty,cidrv4"`
	HostPrefix  int32       `json:"hostPrefix,omitempty"  validate:"omitempty,min=23,max=26"`
}

// ConsoleProfile represents a cluster web console configuration.
// Visibility for the entire struct is "read".
type ConsoleProfile struct {
	URL string `json:"url,omitempty"`
}

// APIProfile represents a cluster API server configuration.
type APIProfile struct {
	URL        string     `json:"url,omitempty"        visibility:"read"`
	Visibility Visibility `json:"visibility,omitempty" visibility:"read create" validate:"omitempty,enum_visibility"`
}

// PlatformProfile represents the Azure platform configuration.
// Visibility for (almost) the entire struct is "read create".
type PlatformProfile struct {
	ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	SubnetID                string                         `json:"subnetId,omitempty"                                  validate:"required_for_put,resource_id=Microsoft.Network/virtualNetworks/subnets"`
	OutboundType            OutboundType                   `json:"outboundType,omitempty"                              validate:"omitempty,enum_outboundtype"`
	NetworkSecurityGroupID  string                         `json:"networkSecurityGroupId,omitempty"                    validate:"required_for_put,resource_id=Microsoft.Network/networkSecurityGroups"`
	OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty"`
	IssuerURL               string                         `json:"issuerUrl,omitempty"               visibility:"read"`
}

// OperatorsAuthenticationProfile represents authentication configuration for
// OpenShift operators.
// Visibility for the entire struct is "read create".
type OperatorsAuthenticationProfile struct {
	UserAssignedIdentities UserAssignedIdentitiesProfile `json:"userAssignedIdentities,omitempty"`
}

// UserAssignedIdentitiesProfile represents authentication configuration for
// OpenShift operators using user-assigned managed identities.
// Visibility for the entire struct is "read create".
type UserAssignedIdentitiesProfile struct {
	ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"  validate:"dive,keys,required,endkeys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"     validate:"dive,keys,required,endkeys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	ServiceManagedIdentity string            `json:"serviceManagedIdentity,omitempty" validate:"omitempty,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
}

// ClusterCapabilitiesProfile - Cluster capabilities configuration.
// Visibility for the entire struct is "read create".
type ClusterCapabilitiesProfile struct {
	// Disabled cluter capabilities.
	Disabled []OptionalClusterCapability `json:"disabled,omitempty" validate:"dive,enum_optionalclustercapability"`
}

// Creates an HCPOpenShiftCluster with any non-zero default values.
func NewDefaultHCPOpenShiftCluster() *HCPOpenShiftCluster {
	return &HCPOpenShiftCluster{
		Identity: arm.ManagedServiceIdentity{
			Type: arm.ManagedServiceIdentityTypeNone,
		},
		Properties: HCPOpenShiftClusterProperties{
			Version: VersionProfile{
				ChannelGroup: "stable",
			},
			Network: NetworkProfile{
				NetworkType: NetworkTypeOVNKubernetes,
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
				HostPrefix:  23,
			},
			API: APIProfile{
				Visibility: VisibilityPublic,
			},
			Platform: PlatformProfile{
				OutboundType: OutboundTypeLoadBalancer,
			},
		},
	}
}

func (cluster *HCPOpenShiftCluster) validateVersion() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	// XXX For now, "stable" is the only accepted value. In the future, we may
	//     allow unlocking other channel groups through Azure Feature Exposure
	//     Control (AFEC) flags or some other mechanism.
	if cluster.Properties.Version.ChannelGroup != "stable" {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: "Channel group must be 'stable'",
			Target:  "properties.version.channelGroup",
		})
	}

	return errorDetails
}

func (cluster *HCPOpenShiftCluster) validateNetworkCIDRs() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody
	var podCIDR, serviceCIDR, machineCIDR *net.IPNet

	// Populated CIDR fields have already passed syntax validation so parsing
	// should not fail. If parsing does fail then skip validating that field.

	_, podCIDR, _ = net.ParseCIDR(cluster.Properties.Network.PodCIDR)
	_, serviceCIDR, _ = net.ParseCIDR(cluster.Properties.Network.ServiceCIDR)
	_, machineCIDR, _ = net.ParseCIDR(cluster.Properties.Network.MachineCIDR)

	// Just check for overlapping subnets. Defer subnet limits to Cluster Service.

	intersect := func(n1, n2 *net.IPNet) bool {
		if n1 == nil || n2 == nil {
			return false
		}

		return n2.Contains(n1.IP) || n1.Contains(n2.IP)
	}

	if intersect(machineCIDR, serviceCIDR) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Machine CIDR '%s' and service CIDR '%s' overlap",
				cluster.Properties.Network.MachineCIDR,
				cluster.Properties.Network.ServiceCIDR),
			Target: "properties.network",
		})
	}

	if intersect(machineCIDR, podCIDR) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Machine CIDR '%s' and pod CIDR '%s' overlap",
				cluster.Properties.Network.MachineCIDR,
				cluster.Properties.Network.PodCIDR),
			Target: "properties.network",
		})
	}

	if intersect(serviceCIDR, podCIDR) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Service CIDR '%s' and pod CIDR '%s' overlap",
				cluster.Properties.Network.ServiceCIDR,
				cluster.Properties.Network.PodCIDR),
			Target: "properties.network",
		})
	}

	return errorDetails
}

func (cluster *HCPOpenShiftCluster) validateManagedResourceGroup(clusterResourceID *azcorearm.ResourceID) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if strings.EqualFold(cluster.Properties.Platform.ManagedResourceGroup, clusterResourceID.ResourceGroupName) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: "Managed resource group name must not be the cluster's resource group name",
			Target:  "properties.platform.managedResourceGroup",
		})
	}

	return errorDetails
}

func (cluster *HCPOpenShiftCluster) validateSubnetID(clusterResourceID *azcorearm.ResourceID) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	// Subnet ID has already passed syntax validation so parsing should
	// not fail. If parsing does somehow fail then skip the validation.

	subnetResourceID, err := azcorearm.ParseResourceID(cluster.Properties.Platform.SubnetID)
	if err != nil {
		return nil
	}

	if !strings.EqualFold(subnetResourceID.SubscriptionID, clusterResourceID.SubscriptionID) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Subnet '%s' must be in the same Azure subscription as the cluster",
				cluster.Properties.Platform.SubnetID),
			Target: "properties.platform.subnetId",
		})
	}

	if cluster.Properties.Platform.ManagedResourceGroup != "" {
		if strings.EqualFold(subnetResourceID.ResourceGroupName, cluster.Properties.Platform.ManagedResourceGroup) {
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code: arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf(
					"Subnet '%s' cannot be in the managed resource group '%s'",
					cluster.Properties.Platform.SubnetID,
					cluster.Properties.Platform.ManagedResourceGroup),
				Target: "properties.platform.subnetId",
			})
		}
	}

	return errorDetails
}

func (cluster *HCPOpenShiftCluster) validateUserAssignedIdentity(clusterResourceID *azcorearm.ResourceID, identity, target string) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	// Managed identity has already passed syntax validation so parsing should
	// not fail. If parsing does somehow fail then skip the validation.

	identityResourceID, err := azcorearm.ParseResourceID(identity)
	if err != nil {
		return nil
	}

	if !strings.EqualFold(identityResourceID.SubscriptionID, clusterResourceID.SubscriptionID) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Identity '%s' must be in the same Azure subscription as the cluster",
				identity),
			Target: target,
		})
	}

	if cluster.Properties.Platform.ManagedResourceGroup != "" {
		if strings.EqualFold(identityResourceID.ResourceGroupName, cluster.Properties.Platform.ManagedResourceGroup) {
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code: arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf(
					"Identity '%s' cannot be in the managed resource group '%s'",
					identity,
					cluster.Properties.Platform.ManagedResourceGroup),
				Target: target,
			})
		}
	}

	return errorDetails
}

func (cluster *HCPOpenShiftCluster) validateUserAssignedIdentities(clusterResourceID *azcorearm.ResourceID) []arm.CloudErrorBody {
	const baseTarget = "properties.platform.operatorsAuthentication.userAssignedIdentities"
	var errorDetails []arm.CloudErrorBody

	serviceManagedIdentity := cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity

	for operatorName, operatorIdentity := range cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		errorDetails = append(errorDetails, cluster.validateUserAssignedIdentity(
			clusterResourceID, operatorIdentity,
			fmt.Sprintf("%s.controlPlaneOperators[%s]", baseTarget, operatorName))...)
	}
	for operatorName, operatorIdentity := range cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		errorDetails = append(errorDetails, cluster.validateUserAssignedIdentity(
			clusterResourceID, operatorIdentity,
			fmt.Sprintf("%s.dataPlaneOperators[%s]", baseTarget, operatorName))...)
	}
	if serviceManagedIdentity != "" {
		errorDetails = append(errorDetails, cluster.validateUserAssignedIdentity(
			clusterResourceID, serviceManagedIdentity,
			fmt.Sprintf("%s.serviceManagedIdentity", baseTarget))...)
	}

	// Verify that every key in Identity.UserAssignedIdentities is referenced
	// exactly once by either ControlPlaneOperators or ServiceManagedIdentity.

	userAssignedIdentities := make(map[string]int)
	for key := range cluster.Identity.UserAssignedIdentities {
		// Resource IDs are case-insensitive. Don't assume they
		// have consistent casing, even within the same resource.
		userAssignedIdentities[strings.ToLower(key)] = 0
	}

	tallyIdentity := func(identity, target string) {
		key := strings.ToLower(identity)
		if _, ok := userAssignedIdentities[key]; ok {
			userAssignedIdentities[key]++
		} else {
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code: arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf(
					"Identity '%s' is not assigned to this resource",
					identity),
				Target: target,
			})
		}
	}

	for operatorName, operatorIdentity := range cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		tallyIdentity(operatorIdentity, baseTarget+fmt.Sprintf(".controlPlaneOperators[%s]", operatorName))
	}

	if serviceManagedIdentity != "" {
		tallyIdentity(serviceManagedIdentity, baseTarget+".serviceManagedIdentity")
	}

	for identity := range cluster.Identity.UserAssignedIdentities {
		key := strings.ToLower(identity)
		if tally, ok := userAssignedIdentities[key]; ok {
			switch tally {
			case 0:
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						"Identity '%s' is assigned to this resource but not used",
						identity),
					Target: "identity.userAssignedIdentities",
				})
			case 1:
				// Valid: Identity is referenced once.
			default:
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						"Identity '%s' is used multiple times",
						identity),
					Target: baseTarget,
				})
			}
		}
	}

	// Data-plane operator identities must not be assigned to this resource.
	for operatorName, operatorIdentity := range cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		key := strings.ToLower(operatorIdentity)
		if _, ok := userAssignedIdentities[key]; ok {
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code: arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf(
					"Data plane operator '%s' cannot use identity assigned to this resource",
					operatorName),
				Target: baseTarget + fmt.Sprintf(".dataPlaneOperators[%s]", operatorName),
			})
		}
	}

	return errorDetails
}

func (cluster *HCPOpenShiftCluster) Validate(validate *validator.Validate, request *http.Request) []arm.CloudErrorBody {
	errorDetails := ValidateRequest(validate, request, cluster)

	// Proceed with complex, multi-field validation only if single-field
	// validation has passed. This avoids running further checks on data
	// we already know to be invalid and prevents the response body from
	// becoming overwhelming.
	if len(errorDetails) == 0 {
		var clusterResourceID *azcorearm.ResourceID

		// This should never fail under normal operating conditions,
		// but there may be unit test cases where this is incomplete.
		if request != nil && request.URL != nil {
			clusterResourceID, _ = azcorearm.ParseResourceID(request.URL.Path)
		}

		errorDetails = append(errorDetails, cluster.validateVersion()...)
		errorDetails = append(errorDetails, cluster.validateNetworkCIDRs()...)

		if clusterResourceID != nil {
			errorDetails = append(errorDetails, cluster.validateManagedResourceGroup(clusterResourceID)...)
			errorDetails = append(errorDetails, cluster.validateSubnetID(clusterResourceID)...)
			errorDetails = append(errorDetails, cluster.validateUserAssignedIdentities(clusterResourceID)...)
		}
	}

	return errorDetails
}
