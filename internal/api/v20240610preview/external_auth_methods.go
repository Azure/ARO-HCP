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
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type ExternalAuth struct {
	generated.ExternalAuth
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
			out.Properties.ProvisioningState = arm.ExternalAuthProvisioningState(*h.Properties.ProvisioningState)
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

func (c *ExternalAuth) ValidateStatic(current api.VersionedHCPOpenShiftClusterExternalAuth, updating bool, request *http.Request) *arm.CloudError {
	var normalized api.HCPOpenShiftClusterExternalAuth
	var errorDetails []arm.CloudErrorBody

	// Pass the embedded ExternalAuth struct so the
	// struct field names match the externalAuthStructTagMap keys.
	errorDetails = api.ValidateVisibility(
		c.ExternalAuth,
		current.(*ExternalAuth).ExternalAuth,
		externalAuthStructTagMap, updating)

	c.Normalize(&normalized)

	// Run additional validation on the "normalized" cluster model.
	errorDetails = append(errorDetails, normalized.Validate(validate, request)...)

	// Returns nil if errorDetails is empty.
	return arm.NewContentValidationError(errorDetails)
}

func normalizeExternalAuthClientProfile(p *generated.ExternalAuthClientProfile, out *api.ExternalAuthClientProfile) {
	if p.Component != nil {
		out.Component.Name = *p.Component.Name
		out.Component.AuthClientNamespace = *p.Component.AuthClientNamespace
	}
	if p.ClientID != nil {
		out.ClientId = *p.ClientID
	}
	out.ExtraScopes = make([]string, len(p.ExtraScopes))
	for i := range p.ExtraScopes {
		if p.ExtraScopes != nil {
			out.ExtraScopes[i] = *p.ExtraScopes[i]
		}
	}
	if p.Type != nil {
		out.ExternalAuthClientProfileType = api.ExternalAuthClientType(*p.Type)
	}
}

func normalizeTokenIssuerProfile(p *generated.TokenIssuerProfile, out *api.TokenIssuerProfile) {
	if p.URL != nil {
		out.Url = p.URL
	}
	out.Audiences = make([]string, len(p.Audiences))
	for i := range p.Audiences {
		if p.Audiences[i] != nil {
			out.Audiences[i] = *p.Audiences[i]
		}
	}
	if p.Ca != nil {
		out.Ca = p.Ca
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
			out.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyType(*p.Username.PrefixPolicy)
		}
	}
	if p.Groups != nil {
		out.Groups = &api.GroupClaimProfile{}
		if p.Groups.Claim != nil {
			out.Groups.Claim = p.Groups.Claim
		}
		if p.Groups.Prefix != nil {
			out.Groups.Prefix = p.Groups.Prefix
		}
	}
}

func normalizeTokenClaimValidationRule(p *generated.TokenClaimValidationRule, out *api.TokenClaimValidationRule) {
	if p.Type != nil {
		out.TokenClaimValidationRuleType = api.TokenValidationRuleType(*p.Type)
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

func newExternalAuthCondition(from *api.ExternalAuthCondition) *generated.ExternalAuthCondition {
	return &generated.ExternalAuthCondition{
		Type:    api.PtrOrNil(generated.ExternalAuthConditionType(from.ConditionType)),
		Status:  api.PtrOrNil(generated.StatusType(from.Status)),
		Reason:  api.PtrOrNil(from.Reason),
		Message: api.PtrOrNil(from.Message),
	}
}

func newTokenIssuerProfile(from *api.TokenIssuerProfile) *generated.TokenIssuerProfile {
	return &generated.TokenIssuerProfile{
		URL:       from.Url,
		Audiences: api.StringSliceToStringPtrSlice(from.Audiences),
		Ca:        from.Ca,
	}
}

func newExternalAuthClaimProfile(from *api.ExternalAuthClaimProfile) *generated.ExternalAuthClaimProfile {
	var groups *generated.GroupClaimProfile

	if from.Mappings.Groups != nil {
		groups = &generated.GroupClaimProfile{
			Claim:  from.Mappings.Groups.Claim,
			Prefix: from.Mappings.Groups.Prefix,
		}
	}

	return &generated.ExternalAuthClaimProfile{
		Mappings: &generated.TokenClaimMappingsProfile{
			Username: &generated.UsernameClaimProfile{
				Claim:        api.PtrOrNil(from.Mappings.Username.Claim),
				Prefix:       api.PtrOrNil(from.Mappings.Username.Prefix),
				PrefixPolicy: api.PtrOrNil(string(from.Mappings.Username.PrefixPolicy)),
			},
			Groups: groups,
		},
	}
}

func (v version) NewHCPOpenShiftClusterExternalAuth(from *api.HCPOpenShiftClusterExternalAuth) api.VersionedHCPOpenShiftClusterExternalAuth {
	if from == nil {
		from = api.NewDefaultHCPOpenShiftClusterExternalAuth()
	}

	out := &ExternalAuth{
		generated.ExternalAuth{
			ID:   api.PtrOrNil(from.ID),
			Name: api.PtrOrNil(from.Name),
			Type: api.PtrOrNil(from.Type),
			Properties: &generated.ExternalAuthProperties{
				ProvisioningState: api.PtrOrNil(generated.ExternalAuthProvisioningState(from.Properties.ProvisioningState)),
				Condition:         newExternalAuthCondition(&from.Properties.Condition),
				Claim:             newExternalAuthClaimProfile(&from.Properties.Claim),
			},
		},
	}
	out.Properties.Issuer = newTokenIssuerProfile(&from.Properties.Issuer)

	if from.SystemData != nil {
		out.SystemData = &generated.SystemData{
			CreatedBy:          api.PtrOrNil(from.SystemData.CreatedBy),
			CreatedByType:      api.PtrOrNil(generated.CreatedByType(from.SystemData.CreatedByType)),
			CreatedAt:          from.SystemData.CreatedAt,
			LastModifiedBy:     api.PtrOrNil(from.SystemData.LastModifiedBy),
			LastModifiedByType: api.PtrOrNil(generated.CreatedByType(from.SystemData.LastModifiedByType)),
			LastModifiedAt:     from.SystemData.LastModifiedAt,
		}
	}

	for _, client := range from.Properties.Clients {
		out.Properties.Clients = append(out.Properties.Clients, &generated.ExternalAuthClientProfile{
			Component: &generated.ExternalAuthClientComponentProfile{
				Name:                api.PtrOrNil(client.Component.Name),
				AuthClientNamespace: api.PtrOrNil(client.Component.AuthClientNamespace),
			},
			ClientID:    api.PtrOrNil(client.ClientId),
			ExtraScopes: api.StringSliceToStringPtrSlice(client.ExtraScopes),
			Type:        api.PtrOrNil(generated.ExternalAuthClientType(client.ExternalAuthClientProfileType)),
		})
	}
	return out
}

func (v version) MarshalHCPOpenShiftClusterExternalAuth(from *api.HCPOpenShiftClusterExternalAuth) ([]byte, error) {
	return arm.MarshalJSON(v.NewHCPOpenShiftClusterExternalAuth(from))
}
