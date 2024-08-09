package adminapi

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftCluster represents an ARO HCP OpenShift cluster resource.
type HCPOpenShiftCluster struct {
	arm.TrackedResource
	Properties HCPOpenShiftClusterProperties `json:"properties,omitempty" validate:"required_for_put"`
}

// HCPOpenShiftClusterProperties represents the property bag of a HCPOpenShiftCluster resource.
type HCPOpenShiftClusterProperties struct {
	ProvisioningState arm.ProvisioningState `json:"provisioningState,omitempty" visibility:"read"               validate:"omitempty,enum_provisioningstate"`
	Spec              ClusterSpec           `json:"spec,omitempty"              visibility:"read create update" validate:"required_for_put"`
}

// ClusterSpec represents a high level cluster configuration.
type ClusterSpec struct {
	Version                       VersionProfile            `json:"version,omitempty"                       visibility:"read create update" validate:"required_for_put"`
	DNS                           DNSProfile                `json:"dns,omitempty"                           visibility:"read create update"`
	Network                       NetworkProfile            `json:"network,omitempty"                       visibility:"read create"`
	Console                       ConsoleProfile            `json:"console,omitempty"                       visibility:"read"`
	API                           APIProfile                `json:"api,omitempty"                           visibility:"read create"        validate:"required_for_put"`
	FIPS                          bool                      `json:"fips,omitempty"                          visibility:"read create"`
	EtcdEncryption                bool                      `json:"etcdEncryption,omitempty"                visibility:"read create"`
	DisableUserWorkloadMonitoring bool                      `json:"disableUserWorkloadMonitoring,omitempty" visibility:"read create update"`
	Proxy                         ProxyProfile              `json:"proxy,omitempty"                         visibility:"read create update"`
	Platform                      PlatformProfile           `json:"platform,omitempty"                      visibility:"read create"        validate:"required_for_put"`
	IssuerURL                     string                    `json:"issuerUrl,omitempty"                     visibility:"read"               validate:"omitempty,url"`
	ExternalAuth                  ExternalAuthConfigProfile `json:"externalAuth,omitempty"                  visibility:"read create"`
	Ingress                       []*IngressProfile         `json:"ingressProfile,omitempty"                visibility:"read create"`
}

// VersionProfile represents the cluster control plane version.
type VersionProfile struct {
	ID                string   `json:"id,omitempty"                visibility:"read create update" validate:"required_for_put"`
	ChannelGroup      string   `json:"channelGroup,omitempty"      visibility:"read create"        validate:"required_for_put"`
	AvailableUpgrades []string `json:"availableUpgrades,omitempty" visibility:"read"`
}

// DNSProfile represents the DNS configuration of the cluster.
type DNSProfile struct {
	BaseDomain       string `json:"baseDomain,omitempty"       visibility:"read"`
	BaseDomainPrefix string `json:"baseDomainPrefix,omitempty" visibility:"read create"`
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
	URL string `json:"url,omitempty" validate:"omitempty,url"`
}

// APIProfile represents a cluster API server configuration.
type APIProfile struct {
	URL        string     `json:"url,omitempty"        visibility:"read"        validate:"omitempty,url"`
	Visibility Visibility `json:"visibility,omitempty" visibility:"read create" validate:"required_for_put,enum_visibility"`
}

// ProxyProfile represents the cluster proxy configuration.
// Visibility for the entire struct is "read create update".
type ProxyProfile struct {
	HTTPProxy  string `json:"httpProxy,omitempty"`
	HTTPSProxy string `json:"httpsProxy,omitempty"`
	NoProxy    string `json:"noProxy,omitempty"`
	TrustedCA  string `json:"trustedCa,omitempty"`
}

// PlatformProfile represents the Azure platform configuration.
// Visibility for the entire struct is "read create".
type PlatformProfile struct {
	ManagedResourceGroup string       `json:"managedResourceGroup,omitempty" validate:"required_for_put"`
	SubnetID             string       `json:"subnetId,omitempty"             validate:"required_for_put"`
	OutboundType         OutboundType `json:"outboundType,omitempty"         validate:"omitempty,enum_outboundtype"`
	//TODO: Is nsg required for PUT, or will we create if not specified?
	NetworkSecurityGroupID string `json:"networkSecurityGroupId,omitempty" validate:"required_for_put"`
	EtcdEncryptionSetID    string `json:"etcdEncryptionSetId,omitempty"`
}

// ExternalAuthConfigProfile represents the external authentication configuration.
type ExternalAuthConfigProfile struct {
	Enabled       bool                     `json:"enabled,omitempty"       visibility:"read create"`
	ExternalAuths []*configv1.OIDCProvider `json:"externalAuths,omitempty" visibility:"read"`
}

// IngressProfile represents a cluster ingress configuration.
type IngressProfile struct {
	IP         string     `json:"ip,omitempty"         visibility:"read"        validate:"omitempty,ipv4"`
	URL        string     `json:"url,omitempty"        visibility:"read"        validate:"omitempty,url"`
	Visibility Visibility `json:"visibility,omitempty" visibility:"read create" validate:"required_for_put,enum_visibility"`
}

// Creates an HCPOpenShiftCluster with any non-zero default values.
func NewDefaultHCPOpenShiftCluster() *HCPOpenShiftCluster {
	return &HCPOpenShiftCluster{
		Properties: HCPOpenShiftClusterProperties{
			Spec: ClusterSpec{
				Network: NetworkProfile{
					NetworkType: NetworkTypeOVNKubernetes,
					HostPrefix:  23,
				},
			},
		},
	}
}

func (c *HCPOpenShiftCluster) Normalize(out *api.HCPOpenShiftCluster) {
	if &c.ID != nil {
		out.Resource.ID = c.ID
	}
	if &c.Name != nil {
		out.Resource.Name = c.Name
	}
	if &c.Type != nil {
		out.Resource.Type = c.Type
	}
	if c.SystemData != nil {
		out.Resource.SystemData = &arm.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if &c.SystemData.CreatedBy != nil {
			out.Resource.SystemData.CreatedBy = c.SystemData.CreatedBy
		}
		if &c.SystemData.CreatedByType != nil {
			out.Resource.SystemData.CreatedByType = arm.CreatedByType(c.SystemData.CreatedByType)
		}
		if &c.SystemData.LastModifiedBy != nil {
			out.Resource.SystemData.LastModifiedBy = c.SystemData.LastModifiedBy
		}
		if &c.SystemData.LastModifiedByType != nil {
			out.Resource.SystemData.LastModifiedByType = arm.CreatedByType(c.SystemData.LastModifiedByType)
		}
	}
	// FIXME Skipping ManagedServiceIdentity
	if &c.Location != nil {
		out.TrackedResource.Location = c.Location
	}
	out.Tags = make(map[string]string)
	for k, v := range c.Tags {
		if v != "" {
			out.Tags[k] = v
		}
	}
	if &c.Properties != nil {
		if &c.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(c.Properties.ProvisioningState)
		}
		if &c.Properties.Spec != nil {
			if &c.Properties.Spec.Version != nil {
				normalizeVersion(&c.Properties.Spec.Version, &out.Properties.Spec.Version)
			}
			if &c.Properties.Spec.DNS != nil {
				normailzeDNS(&c.Properties.Spec.DNS, &out.Properties.Spec.DNS)
			}
			if &c.Properties.Spec.Network != nil {
				normalizeNetwork(&c.Properties.Spec.Network, &out.Properties.Spec.Network)
			}
			if &c.Properties.Spec.Console != nil {
				normalizeConsole(&c.Properties.Spec.Console, &out.Properties.Spec.Console)
			}
			if &c.Properties.Spec.API != nil {
				normalizeAPI(&c.Properties.Spec.API, &out.Properties.Spec.API)
			}
			if &c.Properties.Spec.FIPS != nil {
				out.Properties.Spec.FIPS = c.Properties.Spec.FIPS
			}
			if &c.Properties.Spec.EtcdEncryption != nil {
				out.Properties.Spec.EtcdEncryption = c.Properties.Spec.EtcdEncryption
			}
			if &c.Properties.Spec.DisableUserWorkloadMonitoring != nil {
				out.Properties.Spec.DisableUserWorkloadMonitoring = c.Properties.Spec.DisableUserWorkloadMonitoring
			}
			if &c.Properties.Spec.Proxy != nil {
				normalizeProxy(&c.Properties.Spec.Proxy, &out.Properties.Spec.Proxy)
			}
			if &c.Properties.Spec.Platform != nil {
				normalizePlatform(&c.Properties.Spec.Platform, &out.Properties.Spec.Platform)
			}
			if &c.Properties.Spec.IssuerURL != nil {
				out.Properties.Spec.IssuerURL = c.Properties.Spec.IssuerURL
			}
			if &c.Properties.Spec.ExternalAuth != nil {
				normalizeExternalAuthConfig(&c.Properties.Spec.ExternalAuth, &out.Properties.Spec.ExternalAuth)
			}
			ingressSequence := api.DeleteNilsFromPtrSlice(c.Properties.Spec.Ingress)
			out.Properties.Spec.Ingress = make([]*api.IngressProfile, len(ingressSequence))
			for index, item := range ingressSequence {
				out.Properties.Spec.Ingress[index] = &api.IngressProfile{}
				normalizeIngress(*item, out.Properties.Spec.Ingress[index])
			}
		}
	}
}

func (c *HCPOpenShiftCluster) ValidateStatic(current api.VersionedHCPOpenShiftCluster, updating bool, method string) *arm.CloudError {
	var normalized api.HCPOpenShiftCluster
	var errorDetails []arm.CloudErrorBody

	cloudError := arm.NewCloudError(
		http.StatusBadRequest,
		arm.CloudErrorCodeMultipleErrorsOccurred, "",
		"Content validation failed on multiple fields")
	cloudError.Details = make([]arm.CloudErrorBody, 0)

	// Pass the embedded HcpOpenShiftCluster so the
	// struct field names match the clusterStructTagMap keys.
	instance := HCPOpenShiftCluster{}
	errorDetails = api.ValidateVisibility(
		instance,
		current.(*HCPOpenShiftCluster),
		clusterStructTagMap, updating)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	c.Normalize(&normalized)

	errorDetails = api.ValidateRequest(validate, method, &normalized)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	switch len(cloudError.Details) {
	case 0:
		cloudError = nil
	case 1:
		// Promote a single validation error out of details.
		cloudError.CloudErrorBody = &cloudError.Details[0]
	}

	return cloudError
}

func normalizeVersion(p *VersionProfile, out *api.VersionProfile) {
	if &p.ID != nil {
		out.ID = p.ID
	}
	if &p.ChannelGroup != nil {
		out.ChannelGroup = p.ChannelGroup
	}
	out.AvailableUpgrades = p.AvailableUpgrades
}

func normailzeDNS(p *DNSProfile, out *api.DNSProfile) {
	if &p.BaseDomain != nil {
		out.BaseDomain = p.BaseDomain
	}
	if &p.BaseDomainPrefix != nil {
		out.BaseDomainPrefix = p.BaseDomainPrefix
	}
}

func normalizeNetwork(p *NetworkProfile, out *api.NetworkProfile) {
	if &p.NetworkType != nil {
		out.NetworkType = api.NetworkType(p.NetworkType)
	}
	if &p.PodCIDR != nil {
		out.PodCIDR = p.PodCIDR
	}
	if &p.ServiceCIDR != nil {
		out.ServiceCIDR = p.ServiceCIDR
	}
	if &p.MachineCIDR != nil {
		out.MachineCIDR = p.MachineCIDR
	}
	if &p.HostPrefix != nil {
		out.HostPrefix = p.HostPrefix
	}
}

func normalizeConsole(p *ConsoleProfile, out *api.ConsoleProfile) {
	if &p.URL != nil {
		out.URL = p.URL
	}
}

func normalizeProxy(p *ProxyProfile, out *api.ProxyProfile) {
	if &p.HTTPProxy != nil {
		out.HTTPProxy = p.HTTPProxy
	}
	if &p.HTTPSProxy != nil {
		out.HTTPSProxy = p.HTTPSProxy
	}
	if &p.NoProxy != nil {
		out.NoProxy = p.NoProxy
	}
	if &p.TrustedCA != nil {
		out.TrustedCA = p.TrustedCA
	}
}

func normalizeAPI(p *APIProfile, out *api.APIProfile) {
	if &p.URL != nil {
		out.URL = p.URL
	}
	if &p.Visibility != nil {
		out.Visibility = api.Visibility(p.Visibility)
	}
}

func normalizePlatform(p *PlatformProfile, out *api.PlatformProfile) {
	if &p.ManagedResourceGroup != nil {
		out.ManagedResourceGroup = p.ManagedResourceGroup
	}
	if &p.SubnetID != nil {
		out.SubnetID = p.SubnetID
	}
	if &p.OutboundType != nil {
		out.OutboundType = api.OutboundType(p.OutboundType)
	}
	if &p.NetworkSecurityGroupID != nil {
		out.NetworkSecurityGroupID = p.NetworkSecurityGroupID
	}
	if &p.EtcdEncryptionSetID != nil {
		out.EtcdEncryptionSetID = p.EtcdEncryptionSetID
	}
}

func normalizeExternalAuthConfig(p *ExternalAuthConfigProfile, out *api.ExternalAuthConfigProfile) {
	if &p.Enabled != nil {
		out.Enabled = p.Enabled
	}
	out.ExternalAuths = []*configv1.OIDCProvider{}
	for _, item := range api.DeleteNilsFromPtrSlice(p.ExternalAuths) {
		provider := &configv1.OIDCProvider{}

		if &item.Issuer != nil {
			if &item.Issuer.URL != nil {
				provider.Issuer.URL = item.Issuer.URL
			}
			provider.Issuer.Audiences = make([]configv1.TokenAudience, len(item.Issuer.Audiences))
			for index, audience := range item.Issuer.Audiences {
				if &audience != nil {
					provider.Issuer.Audiences[index] = configv1.TokenAudience(audience)
				}
			}
			if &item.Issuer.CertificateAuthority != nil {
				// Slight misuse of the field. It's meant to name a config map holding a
				// "ca-bundle.crt" key, whereas we store the data directly in the Name field.
				provider.Issuer.CertificateAuthority = configv1.ConfigMapNameReference{
					Name: item.Issuer.CertificateAuthority.Name,
				}
			}
		}

		clientSequence := item.OIDCClients
		provider.OIDCClients = make([]configv1.OIDCClientConfig, len(clientSequence))
		for index, client := range clientSequence {
			if &client != nil {
				if &client.ComponentName != nil {
					provider.OIDCClients[index].ComponentName = client.ComponentName
				}
				if &client.ComponentNamespace != nil {
					provider.OIDCClients[index].ComponentNamespace = client.ComponentNamespace
				}
			}
			if &client.ClientID != nil {
				provider.OIDCClients[index].ClientID = client.ClientID
			}
			if &client.ClientSecret != nil {
				// Slight misuse of the field. It's meant to name a secret holding a
				// "clientSecret" key, whereas we store the data directly in the Name field.
				provider.OIDCClients[index].ClientSecret.Name = client.ClientSecret.Name
			}
			provider.OIDCClients[index].ExtraScopes = client.ExtraScopes
		}

		if item != nil {
			if &item.ClaimMappings != nil {
				if &item.ClaimMappings.Username != nil {
					if &item.ClaimMappings.Username.Claim != nil {
						provider.ClaimMappings.Username.TokenClaimMapping.Claim = item.ClaimMappings.Username.Claim
					}
					if &item.ClaimMappings.Username.PrefixPolicy != nil {
						provider.ClaimMappings.Username.PrefixPolicy = item.ClaimMappings.Username.PrefixPolicy
					}
					if &item.ClaimMappings.Username.Prefix != nil {
						provider.ClaimMappings.Username.Prefix.PrefixString = item.ClaimMappings.Username.Prefix.PrefixString
					}
				}
				if &item.ClaimMappings.Groups != nil {
					if &item.ClaimMappings.Groups.Claim != nil {
						provider.ClaimMappings.Groups.TokenClaimMapping.Claim = item.ClaimMappings.Groups.Claim
					}
					if &item.ClaimMappings.Groups.Prefix != nil {
						provider.ClaimMappings.Groups.Prefix = item.ClaimMappings.Groups.Prefix
					}
				}
			}
		}

		validationRuleSequence := item.ClaimValidationRules
		provider.ClaimValidationRules = make([]configv1.TokenClaimValidationRule, len(validationRuleSequence))
		for index, rule := range validationRuleSequence {
			provider.ClaimValidationRules[index] = configv1.TokenClaimValidationRule{
				Type:          configv1.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: &configv1.TokenRequiredClaim{},
			}
			if &rule.RequiredClaim.Claim != nil {
				provider.ClaimValidationRules[index].RequiredClaim.Claim = rule.RequiredClaim.Claim
			}
			if &rule.RequiredClaim.RequiredValue != nil {
				provider.ClaimValidationRules[index].RequiredClaim.RequiredValue = rule.RequiredClaim.RequiredValue
			}
		}

		out.ExternalAuths = append(out.ExternalAuths, provider)
	}
}

func normalizeIngress(p IngressProfile, out *api.IngressProfile) {
	if &p.IP != nil {
		out.IP = p.IP
	}
	if &p.URL != nil {
		out.URL = p.URL
	}
	if &p.Visibility != nil {
		out.Visibility = api.Visibility(p.Visibility)
	}
}
