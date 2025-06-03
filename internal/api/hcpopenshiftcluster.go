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
	"net/http"

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
	ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"  validate:"dive,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"     validate:"dive,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
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

func (cluster *HCPOpenShiftCluster) validateUserAssignedIdentities() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	// Idea is to check every identity mentioned in the Identity.UserAssignedIdentities is
	// being declared under Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.
	if cluster.Identity.UserAssignedIdentities != nil {
		// Initiate the map that will have the number occurence of ConstrolPlaneOperators fields.
		controlPlaneOpOccurrences := make(map[string]int)
		// Generate a Map of Resource IDs of ControlplaneOperators MI, disregard the DataPlaneOperators.
		for _, operatorResourceID := range cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
			controlPlaneOpOccurrences[operatorResourceID]++
		}
		// variable to hold serviceManagedIdentity
		smiResourceID := cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity

		for operatorName, resourceID := range cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
			_, ok := cluster.Identity.UserAssignedIdentities[resourceID]
			if !ok {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						"Identity %s is not assigned to this resource",
						resourceID),
					Target: fmt.Sprintf("properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[%s]", operatorName),
				})
			} else if controlPlaneOpOccurrences[resourceID] > 1 {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						"Identity %s is used multiple times", resourceID),
					Target: fmt.Sprintf("properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[%s]", operatorName),
				})
			}
		}

		if smiResourceID != "" {
			_, ok := cluster.Identity.UserAssignedIdentities[smiResourceID]
			if !ok {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						"Identity %s is not assigned to this resource",
						smiResourceID),
					Target: "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				})
			}
			// Making sure serviceManagedIdentity is not already assigned to controlPlaneOperators.
			if _, ok := controlPlaneOpOccurrences[smiResourceID]; ok {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						"Identity %s is used multiple times", smiResourceID),
					Target: "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				})
			}
		}

		for resourceID := range cluster.Identity.UserAssignedIdentities {
			if _, ok := controlPlaneOpOccurrences[resourceID]; !ok {
				if smiResourceID != resourceID {
					errorDetails = append(errorDetails, arm.CloudErrorBody{
						Code: arm.CloudErrorCodeInvalidRequestContent,
						Message: fmt.Sprintf(
							"Identity %s is assigned to this resource but not used",
							resourceID),
						Target: "identity.userAssignedIdentities",
					})
				}
			}
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
		errorDetails = append(errorDetails, cluster.validateVersion()...)
		errorDetails = append(errorDetails, cluster.validateUserAssignedIdentities()...)
	}

	return errorDetails
}
