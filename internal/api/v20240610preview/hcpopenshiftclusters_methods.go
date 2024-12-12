package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type HcpOpenShiftClusterResource struct {
	generated.HcpOpenShiftClusterResource
}

func newVersionProfile(from *api.VersionProfile) *generated.VersionProfile {
	return &generated.VersionProfile{
		ID:                api.Ptr(from.ID),
		ChannelGroup:      api.Ptr(from.ChannelGroup),
		AvailableUpgrades: api.StringSliceToStringPtrSlice(from.AvailableUpgrades),
	}
}

func newDNSProfile(from *api.DNSProfile) *generated.DNSProfile {
	return &generated.DNSProfile{
		BaseDomain:       api.Ptr(from.BaseDomain),
		BaseDomainPrefix: api.Ptr(from.BaseDomainPrefix),
	}
}

func newNetworkProfile(from *api.NetworkProfile) *generated.NetworkProfile {
	return &generated.NetworkProfile{
		NetworkType: api.Ptr(generated.NetworkType(from.NetworkType)),
		PodCidr:     api.Ptr(from.PodCIDR),
		ServiceCidr: api.Ptr(from.ServiceCIDR),
		MachineCidr: api.Ptr(from.MachineCIDR),
		HostPrefix:  api.Ptr(from.HostPrefix),
	}
}

func newConsoleProfile(from *api.ConsoleProfile) *generated.ConsoleProfile {
	return &generated.ConsoleProfile{
		URL: api.Ptr(from.URL),
	}
}

func newAPIProfile(from *api.APIProfile) *generated.APIProfile {
	return &generated.APIProfile{
		URL:        api.Ptr(from.URL),
		Visibility: api.Ptr(generated.Visibility(from.Visibility)),
	}
}

func newProxyProfile(from *api.ProxyProfile) *generated.ProxyProfile {
	return &generated.ProxyProfile{
		HTTPProxy:  api.Ptr(from.HTTPProxy),
		HTTPSProxy: api.Ptr(from.HTTPSProxy),
		NoProxy:    api.Ptr(from.NoProxy),
		TrustedCa:  api.Ptr(from.TrustedCA),
	}
}

func newPlatformProfile(from *api.PlatformProfile) *generated.PlatformProfile {
	return &generated.PlatformProfile{
		ManagedResourceGroup:   api.Ptr(from.ManagedResourceGroup),
		SubnetID:               api.Ptr(from.SubnetID),
		OutboundType:           api.Ptr(generated.OutboundType(from.OutboundType)),
		NetworkSecurityGroupID: api.Ptr(from.NetworkSecurityGroupID),
		EtcdEncryptionSetID:    api.Ptr(from.EtcdEncryptionSetID),
	}
}

func newExternalAuthProfile(from *configv1.OIDCProvider) *generated.ExternalAuthProfile {
	out := &generated.ExternalAuthProfile{
		Issuer: &generated.TokenIssuerProfile{
			URL:       api.Ptr(from.Issuer.URL),
			Audiences: make([]*string, len(from.Issuer.Audiences)),
			Ca:        api.Ptr(from.Issuer.CertificateAuthority.Name),
		},
		Clients: make([]*generated.ExternalAuthClientProfile, len(from.OIDCClients)),
		Claim: &generated.ExternalAuthClaimProfile{
			Mappings: &generated.TokenClaimMappingsProfile{
				Username: &generated.ClaimProfile{
					Claim:        api.Ptr(from.ClaimMappings.Username.Claim),
					PrefixPolicy: api.Ptr(string(from.ClaimMappings.Username.PrefixPolicy)),
				},
				Groups: &generated.ClaimProfile{
					Claim:  api.Ptr(from.ClaimMappings.Groups.Claim),
					Prefix: api.Ptr(from.ClaimMappings.Groups.Prefix),
				},
			},
			ValidationRules: make([]*generated.TokenClaimValidationRuleProfile, len(from.ClaimValidationRules)),
		},
	}

	for index, item := range from.Issuer.Audiences {
		out.Issuer.Audiences[index] = api.Ptr(string(item))
	}

	for index, item := range from.OIDCClients {
		out.Clients[index] = newExternalAuthClientProfile(item)
	}

	if from.ClaimMappings.Username.Prefix != nil {
		out.Claim.Mappings.Username.Prefix = api.Ptr(from.ClaimMappings.Username.Prefix.PrefixString)
	}

	for index, item := range from.ClaimValidationRules {
		out.Claim.ValidationRules[index] = newTokenClaimValidationRuleProfile(item)
	}

	return out
}

func newTokenClaimValidationRuleProfile(from configv1.TokenClaimValidationRule) *generated.TokenClaimValidationRuleProfile {
	if from.RequiredClaim == nil {
		// Should never happen since we create these rules.
		panic("TokenClaimValidationRule has no RequiredClaim")
	}

	return &generated.TokenClaimValidationRuleProfile{
		Claim:         api.Ptr(from.RequiredClaim.Claim),
		RequiredValue: api.Ptr(from.RequiredClaim.RequiredValue),
	}
}

func newExternalAuthClientProfile(from configv1.OIDCClientConfig) *generated.ExternalAuthClientProfile {
	return &generated.ExternalAuthClientProfile{
		Component: &generated.ExternalAuthClientComponentProfile{
			Name:                api.Ptr(from.ComponentName),
			AuthClientNamespace: api.Ptr(from.ComponentNamespace),
		},
		ID:          api.Ptr(from.ClientID),
		Secret:      api.Ptr(from.ClientSecret.Name),
		ExtraScopes: api.StringSliceToStringPtrSlice(from.ExtraScopes),
	}
}

func (v version) NewHCPOpenShiftCluster(from *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	if from == nil {
		from = api.NewDefaultHCPOpenShiftCluster()
	}

	out := &HcpOpenShiftClusterResource{
		generated.HcpOpenShiftClusterResource{
			ID:       api.Ptr(from.Resource.ID),
			Name:     api.Ptr(from.Resource.Name),
			Type:     api.Ptr(from.Resource.Type),
			Location: api.Ptr(from.TrackedResource.Location),
			Tags:     api.StringMapToStringPtrMap(from.TrackedResource.Tags),
			// FIXME Skipping ManagedServiceIdentity
			Properties: &generated.HcpOpenShiftClusterProperties{
				ProvisioningState: api.Ptr(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Spec: &generated.ClusterSpec{
					Version:                       newVersionProfile(&from.Properties.Spec.Version),
					DNS:                           newDNSProfile(&from.Properties.Spec.DNS),
					Network:                       newNetworkProfile(&from.Properties.Spec.Network),
					Console:                       newConsoleProfile(&from.Properties.Spec.Console),
					API:                           newAPIProfile(&from.Properties.Spec.API),
					Fips:                          api.Ptr(from.Properties.Spec.FIPS),
					EtcdEncryption:                api.Ptr(from.Properties.Spec.EtcdEncryption),
					DisableUserWorkloadMonitoring: api.Ptr(from.Properties.Spec.DisableUserWorkloadMonitoring),
					Proxy:                         newProxyProfile(&from.Properties.Spec.Proxy),
					Platform:                      newPlatformProfile(&from.Properties.Spec.Platform),
					IssuerURL:                     api.Ptr(from.Properties.Spec.IssuerURL),
					ExternalAuth: &generated.ExternalAuthConfigProfile{
						Enabled:       api.Ptr(from.Properties.Spec.ExternalAuth.Enabled),
						ExternalAuths: make([]*generated.ExternalAuthProfile, len(from.Properties.Spec.ExternalAuth.ExternalAuths)),
					},
				},
			},
		},
	}

	if from.Resource.SystemData != nil {
		out.SystemData = &generated.SystemData{
			CreatedBy:          api.Ptr(from.Resource.SystemData.CreatedBy),
			CreatedByType:      api.Ptr(generated.CreatedByType(from.Resource.SystemData.CreatedByType)),
			CreatedAt:          from.Resource.SystemData.CreatedAt,
			LastModifiedBy:     api.Ptr(from.Resource.SystemData.LastModifiedBy),
			LastModifiedByType: api.Ptr(generated.CreatedByType(from.Resource.SystemData.LastModifiedByType)),
			LastModifiedAt:     from.Resource.SystemData.LastModifiedAt,
		}
	}

	for index, item := range from.Properties.Spec.ExternalAuth.ExternalAuths {
		out.Properties.Spec.ExternalAuth.ExternalAuths[index] = newExternalAuthProfile(item)
	}

	return out
}

func (c *HcpOpenShiftClusterResource) Normalize(out *api.HCPOpenShiftCluster) {
	if c.ID != nil {
		out.Resource.ID = *c.ID
	}
	if c.Name != nil {
		out.Resource.Name = *c.Name
	}
	if c.Type != nil {
		out.Resource.Type = *c.Type
	}
	if c.SystemData != nil {
		out.Resource.SystemData = &arm.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if c.SystemData.CreatedBy != nil {
			out.Resource.SystemData.CreatedBy = *c.SystemData.CreatedBy
		}
		if c.SystemData.CreatedByType != nil {
			out.Resource.SystemData.CreatedByType = arm.CreatedByType(*c.SystemData.CreatedByType)
		}
		if c.SystemData.LastModifiedBy != nil {
			out.Resource.SystemData.LastModifiedBy = *c.SystemData.LastModifiedBy
		}
		if c.SystemData.LastModifiedByType != nil {
			out.Resource.SystemData.LastModifiedByType = arm.CreatedByType(*c.SystemData.LastModifiedByType)
		}
	}
	// FIXME Skipping ManagedServiceIdentity
	if c.Location != nil {
		out.TrackedResource.Location = *c.Location
	}
	out.Tags = api.StringPtrMapToStringMap(c.Tags)
	if c.Properties != nil {
		if c.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(*c.Properties.ProvisioningState)
		}
		if c.Properties.Spec != nil {
			if c.Properties.Spec.Version != nil {
				normalizeVersion(c.Properties.Spec.Version, &out.Properties.Spec.Version)
			}
			if c.Properties.Spec.DNS != nil {
				normailzeDNS(c.Properties.Spec.DNS, &out.Properties.Spec.DNS)
			}
			if c.Properties.Spec.Network != nil {
				normalizeNetwork(c.Properties.Spec.Network, &out.Properties.Spec.Network)
			}
			if c.Properties.Spec.Console != nil {
				normalizeConsole(c.Properties.Spec.Console, &out.Properties.Spec.Console)
			}
			if c.Properties.Spec.API != nil {
				normalizeAPI(c.Properties.Spec.API, &out.Properties.Spec.API)
			}
			if c.Properties.Spec.Fips != nil {
				out.Properties.Spec.FIPS = *c.Properties.Spec.Fips
			}
			if c.Properties.Spec.EtcdEncryption != nil {
				out.Properties.Spec.EtcdEncryption = *c.Properties.Spec.EtcdEncryption
			}
			if c.Properties.Spec.DisableUserWorkloadMonitoring != nil {
				out.Properties.Spec.DisableUserWorkloadMonitoring = *c.Properties.Spec.DisableUserWorkloadMonitoring
			}
			if c.Properties.Spec.Proxy != nil {
				normalizeProxy(c.Properties.Spec.Proxy, &out.Properties.Spec.Proxy)
			}
			if c.Properties.Spec.Platform != nil {
				normalizePlatform(c.Properties.Spec.Platform, &out.Properties.Spec.Platform)
			}
			if c.Properties.Spec.IssuerURL != nil {
				out.Properties.Spec.IssuerURL = *c.Properties.Spec.IssuerURL
			}
			if c.Properties.Spec.ExternalAuth != nil {
				normalizeExternalAuthConfig(c.Properties.Spec.ExternalAuth, &out.Properties.Spec.ExternalAuth)
			}
		}
	}
}

func (c *HcpOpenShiftClusterResource) ValidateStatic(current api.VersionedHCPOpenShiftCluster, updating bool, method string) *arm.CloudError {
	var normalized api.HCPOpenShiftCluster
	var errorDetails []arm.CloudErrorBody

	cloudError := arm.NewCloudError(
		http.StatusBadRequest,
		arm.CloudErrorCodeMultipleErrorsOccurred, "",
		"Content validation failed on multiple fields")
	cloudError.Details = make([]arm.CloudErrorBody, 0)

	// Pass the embedded HcpOpenShiftClusterResource so the
	// struct field names match the clusterStructTagMap keys.
	errorDetails = api.ValidateVisibility(
		c.HcpOpenShiftClusterResource,
		current.(*HcpOpenShiftClusterResource).HcpOpenShiftClusterResource,
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

func normalizeVersion(p *generated.VersionProfile, out *api.VersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
	out.AvailableUpgrades = api.StringPtrSliceToStringSlice(p.AvailableUpgrades)
}

func normailzeDNS(p *generated.DNSProfile, out *api.DNSProfile) {
	if p.BaseDomain != nil {
		out.BaseDomain = *p.BaseDomain
	}
	if p.BaseDomainPrefix != nil {
		out.BaseDomainPrefix = *p.BaseDomainPrefix
	}
}

func normalizeNetwork(p *generated.NetworkProfile, out *api.NetworkProfile) {
	if p.NetworkType != nil {
		out.NetworkType = api.NetworkType(*p.NetworkType)
	}
	if p.PodCidr != nil {
		out.PodCIDR = *p.PodCidr
	}
	if p.ServiceCidr != nil {
		out.ServiceCIDR = *p.ServiceCidr
	}
	if p.MachineCidr != nil {
		out.MachineCIDR = *p.MachineCidr
	}
	if p.HostPrefix != nil {
		out.HostPrefix = *p.HostPrefix
	}
}

func normalizeConsole(p *generated.ConsoleProfile, out *api.ConsoleProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
}

func normalizeAPI(p *generated.APIProfile, out *api.APIProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
	if p.Visibility != nil {
		out.Visibility = api.Visibility(*p.Visibility)
	}
}

func normalizeProxy(p *generated.ProxyProfile, out *api.ProxyProfile) {
	if p.HTTPProxy != nil {
		out.HTTPProxy = *p.HTTPProxy
	}
	if p.HTTPSProxy != nil {
		out.HTTPSProxy = *p.HTTPSProxy
	}
	if p.NoProxy != nil {
		out.NoProxy = *p.NoProxy
	}
	if p.TrustedCa != nil {
		out.TrustedCA = *p.TrustedCa
	}
}

func normalizePlatform(p *generated.PlatformProfile, out *api.PlatformProfile) {
	if p.ManagedResourceGroup != nil {
		out.ManagedResourceGroup = *p.ManagedResourceGroup
	}
	if p.SubnetID != nil {
		out.SubnetID = *p.SubnetID
	}
	if p.OutboundType != nil {
		out.OutboundType = api.OutboundType(*p.OutboundType)
	}
	if p.NetworkSecurityGroupID != nil {
		out.NetworkSecurityGroupID = *p.NetworkSecurityGroupID
	}
	if p.EtcdEncryptionSetID != nil {
		out.EtcdEncryptionSetID = *p.EtcdEncryptionSetID
	}
}

func normalizeExternalAuthConfig(p *generated.ExternalAuthConfigProfile, out *api.ExternalAuthConfigProfile) {
	if p.Enabled != nil {
		out.Enabled = *p.Enabled
	}
	out.ExternalAuths = []*configv1.OIDCProvider{}
	for _, item := range api.DeleteNilsFromPtrSlice(p.ExternalAuths) {
		provider := &configv1.OIDCProvider{}

		if item.Issuer != nil {
			if item.Issuer.URL != nil {
				provider.Issuer.URL = *item.Issuer.URL
			}
			provider.Issuer.Audiences = make([]configv1.TokenAudience, len(item.Issuer.Audiences))
			for index, audience := range item.Issuer.Audiences {
				if audience != nil {
					provider.Issuer.Audiences[index] = configv1.TokenAudience(*audience)
				}
			}
			if item.Issuer.Ca != nil {
				// Slight misuse of the field. It's meant to name a config map holding a
				// "ca-bundle.crt" key, whereas we store the data directly in the Name field.
				provider.Issuer.CertificateAuthority = configv1.ConfigMapNameReference{
					Name: *item.Issuer.Ca,
				}
			}
		}

		clientSequence := api.DeleteNilsFromPtrSlice(item.Clients)
		provider.OIDCClients = make([]configv1.OIDCClientConfig, len(clientSequence))
		for index, client := range clientSequence {
			if client.Component != nil {
				if client.Component.Name != nil {
					provider.OIDCClients[index].ComponentName = *client.Component.Name
				}
				if client.Component.AuthClientNamespace != nil {
					provider.OIDCClients[index].ComponentNamespace = *client.Component.AuthClientNamespace
				}
			}
			if client.ID != nil {
				provider.OIDCClients[index].ClientID = *client.ID
			}
			if client.Secret != nil {
				// Slight misuse of the field. It's meant to name a secret holding a
				// "clientSecret" key, whereas we store the data directly in the Name field.
				provider.OIDCClients[index].ClientSecret.Name = *client.Secret
			}
			provider.OIDCClients[index].ExtraScopes = api.StringPtrSliceToStringSlice(client.ExtraScopes)
		}

		if item.Claim != nil {
			if item.Claim.Mappings != nil {
				if item.Claim.Mappings.Username != nil {
					if item.Claim.Mappings.Username.Claim != nil {
						provider.ClaimMappings.Username.TokenClaimMapping.Claim = *item.Claim.Mappings.Username.Claim
					}
					if item.Claim.Mappings.Username.PrefixPolicy != nil {
						provider.ClaimMappings.Username.PrefixPolicy = configv1.UsernamePrefixPolicy(*item.Claim.Mappings.Username.PrefixPolicy)
					}
					if item.Claim.Mappings.Username.Prefix != nil {
						provider.ClaimMappings.Username.Prefix.PrefixString = *item.Claim.Mappings.Username.Prefix
					}
				}
				if item.Claim.Mappings.Groups != nil {
					if item.Claim.Mappings.Groups.Claim != nil {
						provider.ClaimMappings.Groups.TokenClaimMapping.Claim = *item.Claim.Mappings.Groups.Claim
					}
					if item.Claim.Mappings.Groups.Prefix != nil {
						provider.ClaimMappings.Groups.Prefix = *item.Claim.Mappings.Groups.Prefix
					}
				}
			}
		}

		validationRuleSequence := api.DeleteNilsFromPtrSlice(item.Claim.ValidationRules)
		provider.ClaimValidationRules = make([]configv1.TokenClaimValidationRule, len(validationRuleSequence))
		for index, rule := range validationRuleSequence {
			provider.ClaimValidationRules[index] = configv1.TokenClaimValidationRule{
				Type:          configv1.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: &configv1.TokenRequiredClaim{},
			}
			if rule.Claim != nil {
				provider.ClaimValidationRules[index].RequiredClaim.Claim = *rule.Claim
			}
			if rule.RequiredValue != nil {
				provider.ClaimValidationRules[index].RequiredClaim.RequiredValue = *rule.RequiredValue
			}
		}

		out.ExternalAuths = append(out.ExternalAuths, provider)
	}
}
