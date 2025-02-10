package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	configv1 "github.com/openshift/api/config/v1"

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
	ProvisioningState             arm.ProvisioningState     `json:"provisioningState,omitempty" visibility:"read"`
	Version                       VersionProfile            `json:"version,omitempty"                       visibility:"read create"`
	DNS                           DNSProfile                `json:"dns,omitempty"                           visibility:"read create update"`
	Network                       NetworkProfile            `json:"network,omitempty"                       visibility:"read create"`
	Console                       ConsoleProfile            `json:"console,omitempty"                       visibility:"read"`
	API                           APIProfile                `json:"api,omitempty"                           visibility:"read create"`
	FIPS                          bool                      `json:"fips,omitempty"                          visibility:"read create"`
	EtcdEncryption                bool                      `json:"etcdEncryption,omitempty"                visibility:"read create"`
	DisableUserWorkloadMonitoring bool                      `json:"disableUserWorkloadMonitoring,omitempty" visibility:"read create update"`
	Proxy                         ProxyProfile              `json:"proxy,omitempty"                         visibility:"read create update"`
	Platform                      PlatformProfile           `json:"platform,omitempty"                      visibility:"read create"`
	IssuerURL                     string                    `json:"issuerUrl,omitempty"                     visibility:"read"`
	ExternalAuth                  ExternalAuthConfigProfile `json:"externalAuth,omitempty"                  visibility:"read create"`
}

// VersionProfile represents the cluster control plane version.
type VersionProfile struct {
	ID                string   `json:"id,omitempty"                visibility:"read create" validate:"required_for_put"`
	ChannelGroup      string   `json:"channelGroup,omitempty"      visibility:"read create" validate:"required_for_put"`
	AvailableUpgrades []string `json:"availableUpgrades,omitempty" visibility:"read"`
}

// DNSProfile represents the DNS configuration of the cluster.
type DNSProfile struct {
	BaseDomain       string `json:"baseDomain,omitempty"       visibility:"read"`
	BaseDomainPrefix string `json:"baseDomainPrefix,omitempty" visibility:"read create" validate:"omitempty,dns_rfc1035_label"`
}

// NetworkProfile represents a cluster network configuration.
// Visibility for the entire struct is "read create".
type NetworkProfile struct {
	NetworkType NetworkType `json:"networkType,omitempty"`
	PodCIDR     string      `json:"podCidr,omitempty"     validate:"required_for_put,cidrv4"`
	ServiceCIDR string      `json:"serviceCidr,omitempty" validate:"required_for_put,cidrv4"`
	MachineCIDR string      `json:"machineCidr,omitempty" validate:"required_for_put,cidrv4"`
	HostPrefix  int32       `json:"hostPrefix,omitempty"`
}

// ConsoleProfile represents a cluster web console configuration.
// Visibility for the entire struct is "read".
type ConsoleProfile struct {
	URL string `json:"url,omitempty"`
}

// APIProfile represents a cluster API server configuration.
type APIProfile struct {
	URL        string     `json:"url,omitempty"        visibility:"read"`
	Visibility Visibility `json:"visibility,omitempty" visibility:"read create" validate:"required_for_put,enum_visibility"`
}

// ProxyProfile represents the cluster proxy configuration.
// Visibility for the entire struct is "read create update".
type ProxyProfile struct {
	HTTPProxy  string `json:"httpProxy,omitempty"  validate:"omitempty,url,startswith=http:"`
	HTTPSProxy string `json:"httpsProxy,omitempty" validate:"omitempty,url"`
	NoProxy    string `json:"noProxy,omitempty"`
	TrustedCA  string `json:"trustedCa,omitempty"  validate:"omitempty,pem_certificates"`
}

// PlatformProfile represents the Azure platform configuration.
// Visibility for the entire struct is "read create".
type PlatformProfile struct {
	ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	SubnetID                string                         `json:"subnetId,omitempty"             validate:"required_for_put"`
	OutboundType            OutboundType                   `json:"outboundType,omitempty"         validate:"omitempty,enum_outboundtype"`
	NetworkSecurityGroupID  string                         `json:"networkSecurityGroupId,omitempty"`
	EtcdEncryptionSetID     string                         `json:"etcdEncryptionSetId,omitempty"`
	OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty"`
}

// OperatorsAuthenticationProfile represents authentication configuration for
// OpenShift operators.
type OperatorsAuthenticationProfile struct {
	UserAssignedIdentities UserAssignedIdentitiesProfile `json:"userAssignedIdentities,omitempty"`
}

// UserAssignedIdentitiesProfile represents authentication configuration for
// OpenShift operators using user-assigned managed identities.
type UserAssignedIdentitiesProfile struct {
	ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"  validate:"dive,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"     validate:"dive,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	ServiceManagedIdentity string            `json:"serviceManagedIdentity,omitempty" validate:"omitempty,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
}

// ExternalAuthConfigProfile represents the external authentication configuration.
type ExternalAuthConfigProfile struct {
	Enabled       bool                     `json:"enabled,omitempty"       visibility:"read create"`
	ExternalAuths []*configv1.OIDCProvider `json:"externalAuths,omitempty" visibility:"read"`
}

// Creates an HCPOpenShiftCluster with any non-zero default values.
func NewDefaultHCPOpenShiftCluster() *HCPOpenShiftCluster {
	return &HCPOpenShiftCluster{
		Identity: arm.ManagedServiceIdentity{
			Type: arm.ManagedServiceIdentityTypeNone,
		},
		Properties: HCPOpenShiftClusterProperties{
			Network: NetworkProfile{
				NetworkType: NetworkTypeOVNKubernetes,
				HostPrefix:  23,
			},
			Platform: PlatformProfile{
				OutboundType: OutboundTypeLoadBalancer,
			},
		},
	}
}
