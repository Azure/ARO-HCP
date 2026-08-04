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

package v20261003preview

import (
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261003preview/generated"
)

type ExternalAuth struct {
	generated.ExternalAuth
}

var _ coreapi.VersionedCreatableResource[coreapi.HCPOpenShiftClusterExternalAuth] = &ExternalAuth{}

func (h *ExternalAuth) NewExternal() any {
	return &ExternalAuth{}
}

func SetDefaultValuesExternalAuth(obj *ExternalAuth) {
	if obj.Properties == nil {
		obj.Properties = &generated.ExternalAuthProperties{}
	}
	if obj.Properties.Claim == nil {
		obj.Properties.Claim = &generated.ExternalAuthClaimProfile{}
	}
	if obj.Properties.Claim.Mappings == nil {
		obj.Properties.Claim.Mappings = &generated.TokenClaimMappingsProfile{}
	}
	if obj.Properties.Claim.Mappings.Username == nil {
		obj.Properties.Claim.Mappings.Username = &generated.UsernameClaimProfile{}
	}
	if obj.Properties.Claim.Mappings.Username.PrefixPolicy == nil {
		obj.Properties.Claim.Mappings.Username.PrefixPolicy = ptr.To(generated.UsernameClaimPrefixPolicyNone)
	}
}

func (h *ExternalAuth) GetVersion() coreapi.Version {
	return versionedInterface
}

func (h *ExternalAuth) ConvertToInternal(existing *coreapi.HCPOpenShiftClusterExternalAuth) (*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	out := &coreapi.HCPOpenShiftClusterExternalAuth{}

	if h.ID != nil {
		out.ID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
		out.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
	}
	if h.Name != nil {
		out.Name = *h.Name
	}
	if h.Type != nil {
		out.Type = *h.Type
	}
	if h.SystemData != nil {
		out.SystemData = &coreapi.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = coreapi.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = coreapi.CreatedByType(*h.SystemData.LastModifiedByType)
		}
	}

	if h.Properties != nil {
		if h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = coreapi.ProvisioningState(*h.Properties.ProvisioningState)
		}

		if h.Properties.Issuer != nil {
			normalizeTokenIssuerProfile(h.Properties.Issuer, &out.Properties.Issuer)
		}
		if h.Properties.Claim != nil {
			normalizeExternalAuthClaimProfile(h.Properties.Claim, &out.Properties.Claim)
		}

		out.Properties.Clients = make([]coreapi.ExternalAuthClientProfile, len(h.Properties.Clients))
		for i := range h.Properties.Clients {
			normalizeExternalAuthClientProfile(h.Properties.Clients[i], &out.Properties.Clients[i])
		}
	}

	if existing != nil {
		preserveUnknownExternalAuthFields(existing, out)
	}

	return out, nil
}

// preserveUnknownExternalAuthFields copies customer-facing fields from existing that
// this API version doesn't know about. Currently empty — no cross-version
// customer fields exist yet between v20240610preview and v20260630preview.
func preserveUnknownExternalAuthFields(from, to *coreapi.HCPOpenShiftClusterExternalAuth) {
}

func normalizeExternalAuthClientProfile(p *generated.ExternalAuthClientProfile, out *coreapi.ExternalAuthClientProfile) {
	if p.Component != nil {
		out.Component.Name = ptr.Deref(p.Component.Name, "")
		out.Component.AuthClientNamespace = ptr.Deref(p.Component.AuthClientNamespace, "")
	} else {
		out.Component = coreapi.ExternalAuthClientComponentProfile{}
	}
	out.ClientID = metadataapi.Deref(p.ClientID)
	out.ExtraScopes = make([]string, len(p.ExtraScopes))
	for i := range p.ExtraScopes {
		if p.ExtraScopes[i] != nil {
			out.ExtraScopes[i] = *p.ExtraScopes[i]
		}
	}
	out.Type = metadataapi.ExternalAuthClientType(metadataapi.Deref(p.Type))
}

func normalizeTokenIssuerProfile(p *generated.TokenIssuerProfile, out *coreapi.TokenIssuerProfile) {
	out.URL = metadataapi.Deref(p.URL)
	if p.Audiences != nil {
		out.Audiences = make([]string, len(p.Audiences))
		for i := range p.Audiences {
			if p.Audiences[i] != nil {
				out.Audiences[i] = *p.Audiences[i]
			}
		}
	} else {
		out.Audiences = nil
	}
	out.CA = metadataapi.Deref(p.CA)
}

func normalizeExternalAuthClaimProfile(p *generated.ExternalAuthClaimProfile, out *coreapi.ExternalAuthClaimProfile) {
	if p.Mappings != nil {
		normalizeTokenClaimMappingsProfile(p.Mappings, &out.Mappings)
	} else {
		out.Mappings = coreapi.TokenClaimMappingsProfile{}
	}

	out.ValidationRules = make([]coreapi.TokenClaimValidationRule, len(p.ValidationRules))
	for i := range p.ValidationRules {
		normalizeTokenClaimValidationRule(p.ValidationRules[i], &out.ValidationRules[i])
	}
}

func normalizeTokenClaimMappingsProfile(p *generated.TokenClaimMappingsProfile, out *coreapi.TokenClaimMappingsProfile) {
	if p.Username != nil {
		out.Username.Claim = metadataapi.Deref(p.Username.Claim)
		out.Username.Prefix = metadataapi.Deref(p.Username.Prefix)
		out.Username.PrefixPolicy = metadataapi.UsernameClaimPrefixPolicy(metadataapi.Deref(p.Username.PrefixPolicy))
	} else {
		out.Username = coreapi.UsernameClaimProfile{}
	}
	if p.Groups != nil {
		out.Groups = &coreapi.GroupClaimProfile{
			Claim:  metadataapi.Deref(p.Groups.Claim),
			Prefix: metadataapi.Deref(p.Groups.Prefix),
		}
	} else {
		out.Groups = nil
	}
}

func normalizeTokenClaimValidationRule(p *generated.TokenClaimValidationRule, out *coreapi.TokenClaimValidationRule) {
	out.Type = metadataapi.TokenValidationRuleType(metadataapi.Deref(p.Type))
	if p.RequiredClaim != nil {
		out.RequiredClaim.Claim = metadataapi.Deref(p.RequiredClaim.Claim)
		out.RequiredClaim.RequiredValue = metadataapi.Deref(p.RequiredClaim.RequiredValue)
	} else {
		out.RequiredClaim = coreapi.TokenRequiredClaim{}
	}
}

type HcpOpenShiftClusterExternalAuth struct {
	generated.ExternalAuth
}

func newTokenIssuerProfile(from *coreapi.TokenIssuerProfile) generated.TokenIssuerProfile {
	if from == nil {
		return generated.TokenIssuerProfile{}
	}
	return generated.TokenIssuerProfile{
		URL:       metadataapi.PtrOrNil(from.URL),
		Audiences: metadataapi.StringSliceToStringPtrSlice(from.Audiences),
		CA:        metadataapi.PtrOrNil(from.CA),
	}
}

func newExternalAuthClientComponent(from *coreapi.ExternalAuthClientComponentProfile) generated.ExternalAuthClientComponentProfile {
	if from == nil {
		return generated.ExternalAuthClientComponentProfile{}
	}
	return generated.ExternalAuthClientComponentProfile{
		Name:                metadataapi.PtrOrNil(from.Name),
		AuthClientNamespace: metadataapi.PtrOrNil(from.AuthClientNamespace),
	}
}

func newExternalAuthClaimProfile(from *coreapi.ExternalAuthClaimProfile) generated.ExternalAuthClaimProfile {
	if from == nil {
		return generated.ExternalAuthClaimProfile{}
	}
	return generated.ExternalAuthClaimProfile{
		Mappings:        metadataapi.PtrOrNil(newTokenClaimMappingsProfile(&from.Mappings)),
		ValidationRules: newTokenClaimValidationRules(from.ValidationRules),
	}
}

func newTokenClaimMappingsProfile(from *coreapi.TokenClaimMappingsProfile) generated.TokenClaimMappingsProfile {
	if from == nil {
		return generated.TokenClaimMappingsProfile{}
	}
	return generated.TokenClaimMappingsProfile{
		Username: metadataapi.PtrOrNil(newUsernameClaimProfile(&from.Username)),
		Groups:   newGroupClaimProfile(from.Groups),
	}
}

func newUsernameClaimProfile(from *coreapi.UsernameClaimProfile) generated.UsernameClaimProfile {
	if from == nil {
		return generated.UsernameClaimProfile{}
	}
	return generated.UsernameClaimProfile{
		Claim:        metadataapi.PtrOrNil(from.Claim),
		Prefix:       metadataapi.PtrOrNil(from.Prefix),
		PrefixPolicy: metadataapi.PtrOrNil(generated.UsernameClaimPrefixPolicy(from.PrefixPolicy)),
	}
}

func newGroupClaimProfile(from *coreapi.GroupClaimProfile) *generated.GroupClaimProfile {
	if from == nil {
		return nil
	}
	return &generated.GroupClaimProfile{
		Claim:  metadataapi.PtrOrNil(from.Claim),
		Prefix: metadataapi.PtrOrNil(from.Prefix),
	}
}

func newTokenClaimValidationRules(from []coreapi.TokenClaimValidationRule) []*generated.TokenClaimValidationRule {
	if from == nil {
		return nil
	}
	out := make([]*generated.TokenClaimValidationRule, 0, len(from))
	for _, rule := range from {
		out = append(out, &generated.TokenClaimValidationRule{
			Type:          metadataapi.PtrOrNil(generated.TokenValidationRuleType(rule.Type)),
			RequiredClaim: metadataapi.PtrOrNil(newTokenRequiredClaim(&rule.RequiredClaim)),
		})
	}
	return out
}

func newTokenRequiredClaim(from *coreapi.TokenRequiredClaim) generated.TokenRequiredClaim {
	if from == nil {
		return generated.TokenRequiredClaim{}
	}
	return generated.TokenRequiredClaim{
		Claim:         metadataapi.PtrOrNil(from.Claim),
		RequiredValue: metadataapi.PtrOrNil(from.RequiredValue),
	}
}

func (v version) NewHCPOpenShiftClusterExternalAuth(from *coreapi.HCPOpenShiftClusterExternalAuth) coreapi.VersionedHCPOpenShiftClusterExternalAuth {
	if from == nil {
		ret := &ExternalAuth{}
		SetDefaultValuesExternalAuth(ret)
		return ret
	}

	idString := ""
	if from.ResourceID != nil {
		idString = from.ResourceID.String()
	}

	out := &ExternalAuth{
		generated.ExternalAuth{
			ID:         metadataapi.PtrOrNil(idString),
			Name:       metadataapi.PtrOrNil(from.Name),
			Type:       metadataapi.PtrOrNil(from.Type),
			SystemData: metadataapi.PtrOrNil(newSystemData(from.SystemData)),
			Properties: &generated.ExternalAuthProperties{
				ProvisioningState: metadataapi.PtrOrNil(generated.ExternalAuthProvisioningState(from.Properties.ProvisioningState)),
				Status:            metadataapi.PtrOrNil(newExternalAuthResourceStatus(&from.Status)),
				Issuer:            metadataapi.PtrOrNil(newTokenIssuerProfile(&from.Properties.Issuer)),
				Claim:             metadataapi.PtrOrNil(newExternalAuthClaimProfile(&from.Properties.Claim)),
			},
		},
	}

	for _, client := range from.Properties.Clients {
		out.Properties.Clients = append(out.Properties.Clients, &generated.ExternalAuthClientProfile{
			Component:   metadataapi.PtrOrNil(newExternalAuthClientComponent(&client.Component)),
			ClientID:    metadataapi.PtrOrNil(client.ClientID),
			ExtraScopes: metadataapi.StringSliceToStringPtrSlice(client.ExtraScopes),
			Type:        metadataapi.PtrOrNil(generated.ExternalAuthClientType(client.Type)),
		})
	}
	return out
}

func newExternalAuthResourceStatus(from *coreapi.HCPOpenShiftClusterExternalAuthStatus) generated.ResourceStatus {
	if from == nil {
		return generated.ResourceStatus{}
	}
	return generated.ResourceStatus{
		Conditions: newConditions(from.UserFacingConditions),
	}
}
