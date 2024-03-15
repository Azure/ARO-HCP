package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/json"
)

// HCPOpenShiftCluster represents an ARO HCP OpenShift cluster resource.
type HCPOpenShiftCluster struct {
	arm.TrackedResource
	Properties HCPOpenShiftClusterProperties `json:"properties,omitempty"`
}

// HCPOpenShiftClusterProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterProperties struct {
	ProvisioningState arm.ProvisioningState          `json:"provisioningState,omitempty" visibility:"read"`
	ClusterProfile    ClusterProfile                 `json:"clusterProfile,omitempty"    visibility:"read,create,update"`
	ProxyProfile      ProxyProfile                   `json:"proxyProfile,omitempty"      visibility:"read,create,update"`
	APIProfile        APIProfile                     `json:"apiProfile,omitempty"        visibility:"read,create"`
	ConsoleProfile    ConsoleProfile                 `json:"consoleProfile,omitempty"    visibility:"read,create,update"`
	IngressProfile    IngressProfile                 `json:"ingressProfile,omitempty"    visibility:"read,create"`
	NetworkProfile    NetworkProfile                 `json:"networkProfile,omitempty"    visibility:"read,create"`
	NodePoolProfiles  []api.VersionedNodePoolProfile `json:"nodePoolProfiles,omitempty"  visibility:"read"`
	EtcdEncryption    EtcdEncryptionProfile          `json:"etcdEncryption,omitempty"    visibility:"read,create"`
}

// ClusterProfile represents a high level cluster configuration.
type ClusterProfile struct {
	ControlPlaneVersion  string   `json:"controlPlaneVersion,omitempty"  visibility:"read,create,update"`
	SubnetID             string   `json:"subnetId,omitempty"             visibility:"read,create"`
	ManagedResourceGroup string   `json:"managedResourceGroup,omitempty" visibility:"read,create"`
	OIDCIssuerURL        json.URL `json:"oidcIssuerUrl,omitempty"        visibility:"read"`
}

// ProxyProfile represents the cluster proxy configuration.
// Visibility for the entire struct is "read,create,update".
type ProxyProfile struct {
	HTTPProxy  string `json:"httpProxy,omitempty"`
	HTTPSProxy string `json:"httpsProxy,omitempty"`
	NoProxy    string `json:"noProxy,omitempty"`
	TrustedCA  string `json:"trustedCa,omitempty"`
}

// APIProfile represents a cluster API server configuration.
// Visibility for the entire struct is "read,create".
type APIProfile struct {
	URL        json.URL   `json:"url,omitempty"`
	IP         net.IP     `json:"ip,omitempty"`
	Visibility Visibility `json:"visibility,omitempty"`
}

// ConsoleProfile represents a cluster web console configuration.
type ConsoleProfile struct {
	URL  json.URL `json:"url,omitempty"  visibility:"read"`
	FIPS bool     `json:"fips,omitempty" visibility:"read,create,update"`
}

// IngressProfile represents a cluster ingress configuration.
type IngressProfile struct {
	IP         net.IP     `json:"ip,omitempty"         visibility:"read"`
	URL        json.URL   `json:"url,omitempty"        visibility:"read"`
	Visibility Visibility `json:"visibility,omitempty" visibility:"read,create"`
}

// NetworkProfile represents a cluster network configuration.
// Visibility for the entire struct is "read,create".
type NetworkProfile struct {
	PodCIDR           json.IPNet   `json:"podCidr,omitempty"`
	ServiceCIDR       json.IPNet   `json:"serviceCidr,omitempty"`
	MachineCIDR       json.IPNet   `json:"machineCidr,omitempty"`
	HostPrefix        int32        `json:"hostPrefix,omitempty"`
	OutboundType      OutboundType `json:"outboundType,omitempty"`
	PreconfiguredNSGs bool         `json:"preconfiguredNsgs,omitempty"`
}

// EtcdEncryptionProfile represents the configuration needed for customer
// provided keys to encrypt etcd storage.
// Visibility for the entire struct is "read,create".
type EtcdEncryptionProfile struct {
	DiscEncryptionSetID string `json:"discEncryptionSetId,omitempty"`
}
