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

package v20240610preview

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type ExternalAuth struct {
	generated.ExternalAuth
}

func (h *ExternalAuth) GetVersion() api.Version {
	return versionedInterface
}

func (h *ExternalAuth) Normalize(out *api.HCPOpenShiftClusterExternalAuth) {
	if h.ID != nil {
		out.ID = *h.ID
	}
	if h.Name != nil {
		out.Name = *h.Name
	}
	if h.Type != nil {
		out.Type = *h.Type
	}
	if h.SystemData != nil {
		out.SystemData = &arm.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = arm.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = arm.CreatedByType(*h.SystemData.LastModifiedByType)
		}
	}

	if h.Properties != nil {
		if h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(*h.Properties.ProvisioningState)
		}

		// TODO: Add this when we support Condition
		// if h.Properties.Condition != nil {
		// 	out.Properties.Condition = *h.Properties.Condition
		// }

		if h.Properties.Issuer != nil {
			normalizeTokenIssuerProfile(h.Properties.Issuer, &out.Properties.Issuer)
		}
		if h.Properties.Claim != nil {
			normalizeExternalAuthClaimProfile(h.Properties.Claim, &out.Properties.Claim)
		}

		out.Properties.Clients = make([]api.ExternalAuthClientProfile, len(h.Properties.Clients))
		for i := range h.Properties.Clients {
			normalizeExternalAuthClientProfile(h.Properties.Clients[i], &out.Properties.Clients[i])
		}
	}
}

func (c *ExternalAuth) GetVisibility(path string) (api.VisibilityFlags, bool) {
	flags, ok := externalAuthVisibilityMap[path]
	return flags, ok
}

func (c *ExternalAuth) ValidateVisibility(current api.VersionedCreatableResource[api.HCPOpenShiftClusterExternalAuth], updating bool) []arm.CloudErrorBody {
	var structTagMap = api.GetStructTagMap[api.HCPOpenShiftClusterExternalAuth]()
	return api.ValidateVisibility(c, current.(*ExternalAuth), externalAuthVisibilityMap, structTagMap, updating)
}

func normalizeExternalAuthClientProfile(p *generated.ExternalAuthClientProfile, out *api.ExternalAuthClientProfile) {
	if p.Component != nil {
		out.Component.Name = *p.Component.Name
		out.Component.AuthClientNamespace = *p.Component.AuthClientNamespace
	}
	if p.ClientID != nil {
		out.ClientID = *p.ClientID
	}
	out.ExtraScopes = make([]string, len(p.ExtraScopes))
	for i := range p.ExtraScopes {
		if p.ExtraScopes != nil {
			out.ExtraScopes[i] = *p.ExtraScopes[i]
		}
	}
	if p.Type != nil {
		out.Type = api.ExternalAuthClientType(*p.Type)
	}
}

func normalizeTokenIssuerProfile(p *generated.TokenIssuerProfile, out *api.TokenIssuerProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
	out.Audiences = make([]string, len(p.Audiences))
	for i := range p.Audiences {
		if p.Audiences[i] != nil {
			out.Audiences[i] = *p.Audiences[i]
		}
	}
	if p.CA != nil {
		out.CA = *p.CA
	}
}

func normalizeExternalAuthClaimProfile(p *generated.ExternalAuthClaimProfile, out *api.ExternalAuthClaimProfile) {
	if p.Mappings != nil {
		normalizeTokenClaimMappingsProfile(p.Mappings, &out.Mappings)
	}

	out.ValidationRules = make([]api.TokenClaimValidationRule, len(p.ValidationRules))
	for i := range p.ValidationRules {
		normalizeTokenClaimValidationRule(p.ValidationRules[i], &out.ValidationRules[i])
	}
}

func normalizeTokenClaimMappingsProfile(p *generated.TokenClaimMappingsProfile, out *api.TokenClaimMappingsProfile) {
	if p.Username != nil {

		if p.Username.Claim != nil {
			out.Username.Claim = *p.Username.Claim
		}
		if p.Username.Prefix != nil {
			out.Username.Prefix = *p.Username.Prefix
		}
		if p.Username.PrefixPolicy != nil {
			out.Username.PrefixPolicy = api.UsernameClaimPrefixPolicy(*p.Username.PrefixPolicy)
		}
	}
	if p.Groups != nil {
		out.Groups = &api.GroupClaimProfile{}
		if p.Groups.Claim != nil {
			out.Groups.Claim = *p.Groups.Claim
		}
		if p.Groups.Prefix != nil {
			out.Groups.Prefix = *p.Groups.Prefix
		}
	}
}

func normalizeTokenClaimValidationRule(p *generated.TokenClaimValidationRule, out *api.TokenClaimValidationRule) {
	if p.Type != nil {
		out.Type = api.TokenValidationRuleType(*p.Type)
	}
	if p.RequiredClaim != nil {
		if p.RequiredClaim.Claim != nil {
			out.RequiredClaim.Claim = *p.RequiredClaim.Claim
		}
		if p.RequiredClaim.RequiredValue != nil {
			out.RequiredClaim.RequiredValue = *p.RequiredClaim.RequiredValue
		}
	}
}

type HcpOpenShiftClusterExternalAuth struct {
	generated.ExternalAuth
}

func newExternalAuthCondition(from *api.ExternalAuthCondition) generated.ExternalAuthCondition {
	if from == nil {
		return generated.ExternalAuthCondition{}
	}
	return generated.ExternalAuthCondition{
		Type:    api.PtrOrNil(generated.ExternalAuthConditionType(from.Type)),
		Status:  api.PtrOrNil(generated.StatusType(from.Status)),
		Reason:  api.PtrOrNil(from.Reason),
		Message: api.PtrOrNil(from.Message),
	}
}

func newTokenIssuerProfile(from *api.TokenIssuerProfile) generated.TokenIssuerProfile {
	if from == nil {
		return generated.TokenIssuerProfile{}
	}
	return generated.TokenIssuerProfile{
		URL:       api.PtrOrNil(from.URL),
		Audiences: api.StringSliceToStringPtrSlice(from.Audiences),
		CA:        api.PtrOrNil(from.CA),
	}
}

func newExternalAuthClientComponent(from *api.ExternalAuthClientComponentProfile) generated.ExternalAuthClientComponentProfile {
	if from == nil {
		return generated.ExternalAuthClientComponentProfile{}
	}
	return generated.ExternalAuthClientComponentProfile{
		Name:                api.PtrOrNil(from.Name),
		AuthClientNamespace: api.PtrOrNil(from.AuthClientNamespace),
	}
}

func newExternalAuthClaimProfile(from *api.ExternalAuthClaimProfile) generated.ExternalAuthClaimProfile {
	if from == nil {
		return generated.ExternalAuthClaimProfile{}
	}
	return generated.ExternalAuthClaimProfile{
		Mappings:        api.PtrOrNil(newTokenClaimMappingsProfile(&from.Mappings)),
		ValidationRules: newTokenClaimValidationRules(from.ValidationRules),
	}
}

func newTokenClaimMappingsProfile(from *api.TokenClaimMappingsProfile) generated.TokenClaimMappingsProfile {
	if from == nil {
		return generated.TokenClaimMappingsProfile{}
	}
	return generated.TokenClaimMappingsProfile{
		Username: api.PtrOrNil(newUsernameClaimProfile(&from.Username)),
		Groups:   api.PtrOrNil(newGroupClaimProfile(from.Groups)),
	}
}

func newUsernameClaimProfile(from *api.UsernameClaimProfile) generated.UsernameClaimProfile {
	if from == nil {
		return generated.UsernameClaimProfile{}
	}
	return generated.UsernameClaimProfile{
		Claim:        api.PtrOrNil(from.Claim),
		Prefix:       api.PtrOrNil(from.Prefix),
		PrefixPolicy: api.PtrOrNil(generated.UsernameClaimPrefixPolicy(from.PrefixPolicy)),
	}
}

func newGroupClaimProfile(from *api.GroupClaimProfile) generated.GroupClaimProfile {
	if from == nil {
		return generated.GroupClaimProfile{}
	}
	return generated.GroupClaimProfile{
		Claim:  api.PtrOrNil(from.Claim),
		Prefix: api.PtrOrNil(from.Prefix),
	}
}

func newTokenClaimValidationRules(from []api.TokenClaimValidationRule) []*generated.TokenClaimValidationRule {
	if from == nil {
		return nil
	}
	out := make([]*generated.TokenClaimValidationRule, 0, len(from))
	for _, rule := range from {
		out = append(out, &generated.TokenClaimValidationRule{
			Type:          api.PtrOrNil(generated.TokenValidationRuleType(rule.Type)),
			RequiredClaim: api.PtrOrNil(newTokenRequiredClaim(&rule.RequiredClaim)),
		})
	}
	return out
}

func newTokenRequiredClaim(from *api.TokenRequiredClaim) generated.TokenRequiredClaim {
	if from == nil {
		return generated.TokenRequiredClaim{}
	}
	return generated.TokenRequiredClaim{
		Claim:         api.PtrOrNil(from.Claim),
		RequiredValue: api.PtrOrNil(from.RequiredValue),
	}
}

func (v version) NewHCPOpenShiftClusterExternalAuth(from *api.HCPOpenShiftClusterExternalAuth) api.VersionedHCPOpenShiftClusterExternalAuth {
	if from == nil {
		from = api.NewDefaultHCPOpenShiftClusterExternalAuth()
	}

	out := &ExternalAuth{
		generated.ExternalAuth{
			ID:         api.PtrOrNil(from.ID),
			Name:       api.PtrOrNil(from.Name),
			Type:       api.PtrOrNil(from.Type),
			SystemData: api.PtrOrNil(newSystemData(from.SystemData)),
			Properties: &generated.ExternalAuthProperties{
				ProvisioningState: api.PtrOrNil(generated.ExternalAuthProvisioningState(from.Properties.ProvisioningState)),
				Condition:         api.PtrOrNil(newExternalAuthCondition(&from.Properties.Condition)),
				Issuer:            api.PtrOrNil(newTokenIssuerProfile(&from.Properties.Issuer)),
				Claim:             api.PtrOrNil(newExternalAuthClaimProfile(&from.Properties.Claim)),
			},
		},
	}

	for _, client := range from.Properties.Clients {
		out.Properties.Clients = append(out.Properties.Clients, &generated.ExternalAuthClientProfile{
			Component:   api.PtrOrNil(newExternalAuthClientComponent(&client.Component)),
			ClientID:    api.PtrOrNil(client.ClientID),
			ExtraScopes: api.StringSliceToStringPtrSlice(client.ExtraScopes),
			Type:        api.PtrOrNil(generated.ExternalAuthClientType(client.Type)),
		})
	}
	return out
}
