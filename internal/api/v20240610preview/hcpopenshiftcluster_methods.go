package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"slices"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
)

func newIngressProfile(from *api.IngressProfile) *IngressProfile {
	return &IngressProfile{
		IP:         from.IP,
		URL:        from.URL,
		Visibility: Visibility(from.Visibility),
	}
}

func newExternalAuthProfile(from *configv1.OIDCProvider) *ExternalAuthProfile {
	out := &ExternalAuthProfile{
		Issuer: TokenIssuerProfile{
			URL:       from.Issuer.URL,
			Audiences: make([]string, 0, len(from.Issuer.Audiences)),
			CA:        from.Issuer.CertificateAuthority.Name,
		},
		Clients: make([]*ExternalAuthClientProfile, 0, len(from.OIDCClients)),
		Claim: ExternalAuthClaimProfile{
			Mappings: TokenClaimMappingsProfile{
				Username: ClaimProfile{
					Claim:        from.ClaimMappings.Username.Claim,
					PrefixPolicy: string(from.ClaimMappings.Username.PrefixPolicy),
				},
				Groups: ClaimProfile{
					Claim:  from.ClaimMappings.Groups.Claim,
					Prefix: from.ClaimMappings.Groups.Prefix,
				},
			},
			ValidationRules: make([]*TokenClaimValidationRuleProfile, 0, len(from.ClaimValidationRules)),
		},
	}

	for _, item := range from.Issuer.Audiences {
		out.Issuer.Audiences = append(out.Issuer.Audiences, string(item))
	}

	for _, item := range from.OIDCClients {
		out.Clients = append(out.Clients, newExternalAuthClientProfile(item))
	}

	if from.ClaimMappings.Username.Prefix != nil {
		out.Claim.Mappings.Username.Prefix = from.ClaimMappings.Username.Prefix.PrefixString
	}

	for _, item := range from.ClaimValidationRules {
		out.Claim.ValidationRules = append(out.Claim.ValidationRules, newTokenClaimValidationRuleProfile(item))
	}

	return out
}

func newTokenClaimValidationRuleProfile(from configv1.TokenClaimValidationRule) *TokenClaimValidationRuleProfile {
	if from.RequiredClaim == nil {
		// Should never happen since we create these rules.
		panic("TokenClaimValidationRule has no RequiredClaim")
	}

	return &TokenClaimValidationRuleProfile{
		Claim:         from.RequiredClaim.Claim,
		RequiredValue: from.RequiredClaim.RequiredValue,
	}
}

func newExternalAuthClientProfile(from configv1.OIDCClientConfig) *ExternalAuthClientProfile {
	return &ExternalAuthClientProfile{
		Component: ExternalAuthClientComponentProfile{
			Name:                from.ComponentName,
			AuthClientNamespace: from.ComponentNamespace,
		},
		ID:          from.ClientID,
		Secret:      from.ClientSecret.Name,
		ExtraScopes: slices.Clone(from.ExtraScopes),
	}
}

func (v version) NewHCPOpenShiftCluster(from *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	out := &HCPOpenShiftCluster{
		Properties: HCPOpenShiftClusterProperties{
			ProvisioningState: from.Properties.ProvisioningState,
			Spec: ClusterSpec{
				Version: VersionProfile{
					ID:                from.Properties.Spec.Version.ID,
					ChannelGroup:      from.Properties.Spec.Version.ChannelGroup,
					AvailableUpgrades: slices.Clone(from.Properties.Spec.Version.AvailableUpgrades),
				},
				DNS: DNSProfile{
					BaseDomain:       from.Properties.Spec.DNS.BaseDomain,
					BaseDomainPrefix: from.Properties.Spec.DNS.BaseDomainPrefix,
				},
				Network: NetworkProfile{
					NetworkType: NetworkType(from.Properties.Spec.Network.NetworkType),
					PodCIDR:     from.Properties.Spec.Network.PodCIDR,
					ServiceCIDR: from.Properties.Spec.Network.ServiceCIDR,
					MachineCIDR: from.Properties.Spec.Network.MachineCIDR,
					HostPrefix:  from.Properties.Spec.Network.HostPrefix,
				},
				Console: ConsoleProfile{
					URL: from.Properties.Spec.Console.URL,
				},
				API: APIProfile{
					URL:        from.Properties.Spec.API.URL,
					IP:         from.Properties.Spec.API.IP,
					Visibility: Visibility(from.Properties.Spec.API.Visibility),
				},
				FIPS:                          from.Properties.Spec.FIPS,
				EtcdEncryption:                from.Properties.Spec.EtcdEncryption,
				DisableUserWorkloadMonitoring: from.Properties.Spec.DisableUserWorkloadMonitoring,
				Proxy: ProxyProfile{
					HTTPProxy:  from.Properties.Spec.Proxy.HTTPProxy,
					HTTPSProxy: from.Properties.Spec.Proxy.HTTPSProxy,
					NoProxy:    from.Properties.Spec.Proxy.NoProxy,
					TrustedCA:  from.Properties.Spec.Proxy.TrustedCA,
				},
				Platform: PlatformProfile{
					ManagedResourceGroup: from.Properties.Spec.Platform.ManagedResourceGroup,
					SubnetID:             from.Properties.Spec.Platform.SubnetID,
					OutboundType:         OutboundType(from.Properties.Spec.Platform.OutboundType),
					PreconfiguredNSGs:    from.Properties.Spec.Platform.PreconfiguredNSGs,
					EtcdEncryptionSetID:  from.Properties.Spec.Platform.EtcdEncryptionSetID,
				},
				IssuerURL: from.Properties.Spec.IssuerURL,
				ExternalAuth: ExternalAuthConfigProfile{
					Enabled:       from.Properties.Spec.ExternalAuth.Enabled,
					ExternalAuths: make([]*ExternalAuthProfile, 0, len(from.Properties.Spec.ExternalAuth.ExternalAuths)),
				},
				Ingress: make([]*IngressProfile, 0, len(from.Properties.Spec.Ingress)),
			},
		},
	}

	out.TrackedResource.Copy(&from.TrackedResource)

	for _, item := range from.Properties.Spec.ExternalAuth.ExternalAuths {
		out.Properties.Spec.ExternalAuth.ExternalAuths = append(
			out.Properties.Spec.ExternalAuth.ExternalAuths, newExternalAuthProfile(item))
	}

	for _, item := range from.Properties.Spec.Ingress {
		out.Properties.Spec.Ingress = append(
			out.Properties.Spec.Ingress, newIngressProfile(item))
	}

	return out
}

func (c *HCPOpenShiftCluster) Normalize(out *api.HCPOpenShiftCluster) {
	c.TrackedResource.Copy(&out.TrackedResource)
	out.Properties.ProvisioningState = c.Properties.ProvisioningState
	c.Properties.Spec.Version.Normalize(&out.Properties.Spec.Version)
	c.Properties.Spec.DNS.Normalize(&out.Properties.Spec.DNS)
	c.Properties.Spec.Network.Normalize(&out.Properties.Spec.Network)
	c.Properties.Spec.Console.Normalize(&out.Properties.Spec.Console)
	c.Properties.Spec.API.Normalize(&out.Properties.Spec.API)
	out.Properties.Spec.FIPS = c.Properties.Spec.FIPS
	out.Properties.Spec.EtcdEncryption = c.Properties.Spec.EtcdEncryption
	out.Properties.Spec.DisableUserWorkloadMonitoring = c.Properties.Spec.DisableUserWorkloadMonitoring
	c.Properties.Spec.Proxy.Normalize(&out.Properties.Spec.Proxy)
	c.Properties.Spec.Platform.Normalize(&out.Properties.Spec.Platform)
	out.Properties.Spec.IssuerURL = c.Properties.Spec.IssuerURL
	c.Properties.Spec.ExternalAuth.Normalize(&out.Properties.Spec.ExternalAuth)
	out.Properties.Spec.Ingress = make([]*api.IngressProfile, 0, len(c.Properties.Spec.Ingress))
	for _, item := range c.Properties.Spec.Ingress {
		out.Properties.Spec.Ingress = append(
			out.Properties.Spec.Ingress, item.Normalize())
	}
}

func (c *HCPOpenShiftCluster) ValidateStatic() error {
	return nil
}

func (p *VersionProfile) Normalize(out *api.VersionProfile) {
	out.ID = p.ID
	out.ChannelGroup = p.ChannelGroup
	out.AvailableUpgrades = slices.Clone(p.AvailableUpgrades)
}

func (p *DNSProfile) Normalize(out *api.DNSProfile) {
	out.BaseDomain = p.BaseDomain
	out.BaseDomainPrefix = p.BaseDomainPrefix
}

func (p *NetworkProfile) Normalize(out *api.NetworkProfile) {
	out.NetworkType = api.NetworkType(p.NetworkType)
	out.PodCIDR = p.PodCIDR
	out.ServiceCIDR = p.ServiceCIDR
	out.MachineCIDR = p.MachineCIDR
	out.HostPrefix = p.HostPrefix
}

func (p *ConsoleProfile) Normalize(out *api.ConsoleProfile) {
	out.URL = p.URL
}

func (p *APIProfile) Normalize(out *api.APIProfile) {
	out.URL = p.URL
	out.IP = p.IP
	out.Visibility = api.Visibility(p.Visibility)
}

func (p *ProxyProfile) Normalize(out *api.ProxyProfile) {
	out.HTTPProxy = p.HTTPProxy
	out.HTTPSProxy = p.HTTPSProxy
	out.NoProxy = p.NoProxy
	out.TrustedCA = p.TrustedCA
}

func (p *PlatformProfile) Normalize(out *api.PlatformProfile) {
	out.ManagedResourceGroup = p.ManagedResourceGroup
	out.SubnetID = p.SubnetID
	out.OutboundType = api.OutboundType(p.OutboundType)
	out.PreconfiguredNSGs = p.PreconfiguredNSGs
	out.EtcdEncryptionSetID = p.EtcdEncryptionSetID
}

func (p *ExternalAuthConfigProfile) Normalize(out *api.ExternalAuthConfigProfile) {
	out.Enabled = p.Enabled
	out.ExternalAuths = make([]*configv1.OIDCProvider, 0, len(p.ExternalAuths))
	for _, item := range p.ExternalAuths {
		provider := &configv1.OIDCProvider{
			Issuer: configv1.TokenIssuer{
				URL:       item.Issuer.URL,
				Audiences: make([]configv1.TokenAudience, len(item.Issuer.Audiences)),
				// Slight misuse of the field. It's meant to name a config map holding a
				// "ca-bundle.crt" key, whereas we store the data directly in the Name field.
				CertificateAuthority: configv1.ConfigMapNameReference{
					Name: item.Issuer.CA,
				},
			},
			OIDCClients: make([]configv1.OIDCClientConfig, len(item.Clients)),
			ClaimMappings: configv1.TokenClaimMappings{
				Username: configv1.UsernameClaimMapping{
					TokenClaimMapping: configv1.TokenClaimMapping{
						Claim: item.Claim.Mappings.Username.Claim,
					},
					PrefixPolicy: configv1.UsernamePrefixPolicy(item.Claim.Mappings.Username.PrefixPolicy),
					Prefix: &configv1.UsernamePrefix{
						PrefixString: item.Claim.Mappings.Username.Prefix,
					},
				},
				Groups: configv1.PrefixedClaimMapping{
					TokenClaimMapping: configv1.TokenClaimMapping{
						Claim: item.Claim.Mappings.Groups.Claim,
					},
					Prefix: item.Claim.Mappings.Groups.Prefix,
				},
			},
			ClaimValidationRules: make([]configv1.TokenClaimValidationRule, len(item.Claim.ValidationRules)),
		}

		for index, audience := range item.Issuer.Audiences {
			provider.Issuer.Audiences[index] = configv1.TokenAudience(audience)
		}

		for index, client := range item.Clients {
			provider.OIDCClients[index] = configv1.OIDCClientConfig{
				ComponentName:      client.Component.Name,
				ComponentNamespace: client.Component.AuthClientNamespace,
				ClientID:           client.ID,
				// Slight misuse of the field. It's meant to name a secret holding a
				// "clientSecret" key, whereas we store the data directly in the Name field.
				ClientSecret: configv1.SecretNameReference{
					Name: client.Secret,
				},
				ExtraScopes: slices.Clone(client.ExtraScopes),
			}
		}

		for index, rule := range item.Claim.ValidationRules {
			provider.ClaimValidationRules[index] = configv1.TokenClaimValidationRule{
				Type: configv1.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: &configv1.TokenRequiredClaim{
					Claim:         rule.Claim,
					RequiredValue: rule.RequiredValue,
				},
			}
		}

		out.ExternalAuths = append(out.ExternalAuths, provider)
	}
}

func (p *IngressProfile) Normalize() *api.IngressProfile {
	return &api.IngressProfile{
		IP:         p.IP,
		URL:        p.URL,
		Visibility: api.Visibility(p.Visibility),
	}
}
