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
	"strings"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

type ExternalAuth struct {
	generated.ExternalAuth
}

var _ resourcesapi.VersionedCreatableResource[resourcesapi.HCPOpenShiftClusterExternalAuth] = &ExternalAuth{}

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

func (h *ExternalAuth) GetVersion() resourcesapi.Version {
	return versionedInterface
}

func (h *ExternalAuth) ConvertToInternal(existing *resourcesapi.HCPOpenShiftClusterExternalAuth) (*resourcesapi.HCPOpenShiftClusterExternalAuth, error) {
	out := &resourcesapi.HCPOpenShiftClusterExternalAuth{}

	if h.ID != nil {
		out.ID = resourcesapi.Must(azcorearm.ParseResourceID(strings.ToLower(*h.ID)))
	}
	if h.Name != nil {
		out.Name = *h.Name
	}
	if h.Type != nil {
		out.Type = *h.Type
	}
	if h.SystemData != nil {
		out.SystemData = &armresourcesapi.SystemData{
			CreatedAt:      h.SystemData.CreatedAt,
			LastModifiedAt: h.SystemData.LastModifiedAt,
		}
		if h.SystemData.CreatedBy != nil {
			out.SystemData.CreatedBy = *h.SystemData.CreatedBy
		}
		if h.SystemData.CreatedByType != nil {
			out.SystemData.CreatedByType = armresourcesapi.CreatedByType(*h.SystemData.CreatedByType)
		}
		if h.SystemData.LastModifiedBy != nil {
			out.SystemData.LastModifiedBy = *h.SystemData.LastModifiedBy
		}
		if h.SystemData.LastModifiedByType != nil {
			out.SystemData.LastModifiedByType = armresourcesapi.CreatedByType(*h.SystemData.LastModifiedByType)
		}
	}

	if h.Properties != nil {
		if h.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = armresourcesapi.ProvisioningState(*h.Properties.ProvisioningState)
		}

		if h.Properties.Condition != nil {
			out.Properties.Condition = resourcesapi.ExternalAuthCondition{
				Type:               resourcesapi.ExternalAuthConditionType(ptr.Deref(h.Properties.Condition.Type, "")),
				Status:             resourcesapi.ConditionStatusType(ptr.Deref(h.Properties.Condition.Status, "")),
				LastTransitionTime: ptr.Deref(h.Properties.Condition.LastTransitionTime, time.Time{}),
				Reason:             ptr.Deref(h.Properties.Condition.Reason, ""),
				Message:            ptr.Deref(h.Properties.Condition.Message, ""),
			}
		}

		if h.Properties.Issuer != nil {
			normalizeTokenIssuerProfile(h.Properties.Issuer, &out.Properties.Issuer)
		}
		if h.Properties.Claim != nil {
			normalizeExternalAuthClaimProfile(h.Properties.Claim, &out.Properties.Claim)
		}

		out.Properties.Clients = make([]resourcesapi.ExternalAuthClientProfile, len(h.Properties.Clients))
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
// customer fields exist yet between v20240610preview and v20251223preview.
func preserveUnknownExternalAuthFields(from, to *resourcesapi.HCPOpenShiftClusterExternalAuth) {
}

func normalizeExternalAuthClientProfile(p *generated.ExternalAuthClientProfile, out *resourcesapi.ExternalAuthClientProfile) {
	if p.Component != nil {
		out.Component.Name = ptr.Deref(p.Component.Name, "")
		out.Component.AuthClientNamespace = ptr.Deref(p.Component.AuthClientNamespace, "")
	}
	if p.ClientID != nil {
		out.ClientID = *p.ClientID
	}
	out.ExtraScopes = make([]string, len(p.ExtraScopes))
	for i := range p.ExtraScopes {
		if p.ExtraScopes[i] != nil {
			out.ExtraScopes[i] = *p.ExtraScopes[i]
		}
	}
	if p.Type != nil {
		out.Type = resourcesapi.ExternalAuthClientType(*p.Type)
	}
}

func normalizeTokenIssuerProfile(p *generated.TokenIssuerProfile, out *resourcesapi.TokenIssuerProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
	if p.Audiences != nil {
		out.Audiences = make([]string, len(p.Audiences))
		for i := range p.Audiences {
			if p.Audiences[i] != nil {
				out.Audiences[i] = *p.Audiences[i]
			}
		}
	}
	if p.CA != nil {
		out.CA = *p.CA
	}
}

func normalizeExternalAuthClaimProfile(p *generated.ExternalAuthClaimProfile, out *resourcesapi.ExternalAuthClaimProfile) {
	if p.Mappings != nil {
		normalizeTokenClaimMappingsProfile(p.Mappings, &out.Mappings)
	}

	out.ValidationRules = make([]resourcesapi.TokenClaimValidationRule, len(p.ValidationRules))
	for i := range p.ValidationRules {
		normalizeTokenClaimValidationRule(p.ValidationRules[i], &out.ValidationRules[i])
	}
}

func normalizeTokenClaimMappingsProfile(p *generated.TokenClaimMappingsProfile, out *resourcesapi.TokenClaimMappingsProfile) {
	if p.Username != nil {

		if p.Username.Claim != nil {
			out.Username.Claim = *p.Username.Claim
		}
		if p.Username.Prefix != nil {
			out.Username.Prefix = *p.Username.Prefix
		}
		if p.Username.PrefixPolicy != nil {
			out.Username.PrefixPolicy = resourcesapi.UsernameClaimPrefixPolicy(*p.Username.PrefixPolicy)
		}
	}
	if p.Groups != nil {
		out.Groups = &resourcesapi.GroupClaimProfile{}
		if p.Groups.Claim != nil {
			out.Groups.Claim = *p.Groups.Claim
		}
		if p.Groups.Prefix != nil {
			out.Groups.Prefix = *p.Groups.Prefix
		}
	}
}

func normalizeTokenClaimValidationRule(p *generated.TokenClaimValidationRule, out *resourcesapi.TokenClaimValidationRule) {
	if p.Type != nil {
		out.Type = resourcesapi.TokenValidationRuleType(*p.Type)
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

func newExternalAuthCondition(from *resourcesapi.ExternalAuthCondition) generated.ExternalAuthCondition {
	if from == nil {
		return generated.ExternalAuthCondition{}
	}
	return generated.ExternalAuthCondition{
		Type:               resourcesapi.PtrOrNil(generated.ExternalAuthConditionType(from.Type)),
		Status:             resourcesapi.PtrOrNil(generated.StatusType(from.Status)),
		LastTransitionTime: resourcesapi.PtrOrNil(from.LastTransitionTime),
		Reason:             resourcesapi.PtrOrNil(from.Reason),
		Message:            resourcesapi.PtrOrNil(from.Message),
	}
}

func newTokenIssuerProfile(from *resourcesapi.TokenIssuerProfile) generated.TokenIssuerProfile {
	if from == nil {
		return generated.TokenIssuerProfile{}
	}
	return generated.TokenIssuerProfile{
		URL:       resourcesapi.PtrOrNil(from.URL),
		Audiences: resourcesapi.StringSliceToStringPtrSlice(from.Audiences),
		CA:        resourcesapi.PtrOrNil(from.CA),
	}
}

func newExternalAuthClientComponent(from *resourcesapi.ExternalAuthClientComponentProfile) generated.ExternalAuthClientComponentProfile {
	if from == nil {
		return generated.ExternalAuthClientComponentProfile{}
	}
	return generated.ExternalAuthClientComponentProfile{
		Name:                resourcesapi.PtrOrNil(from.Name),
		AuthClientNamespace: resourcesapi.PtrOrNil(from.AuthClientNamespace),
	}
}

func newExternalAuthClaimProfile(from *resourcesapi.ExternalAuthClaimProfile) generated.ExternalAuthClaimProfile {
	if from == nil {
		return generated.ExternalAuthClaimProfile{}
	}
	return generated.ExternalAuthClaimProfile{
		Mappings:        resourcesapi.PtrOrNil(newTokenClaimMappingsProfile(&from.Mappings)),
		ValidationRules: newTokenClaimValidationRules(from.ValidationRules),
	}
}

func newTokenClaimMappingsProfile(from *resourcesapi.TokenClaimMappingsProfile) generated.TokenClaimMappingsProfile {
	if from == nil {
		return generated.TokenClaimMappingsProfile{}
	}
	return generated.TokenClaimMappingsProfile{
		Username: resourcesapi.PtrOrNil(newUsernameClaimProfile(&from.Username)),
		Groups:   newGroupClaimProfile(from.Groups),
	}
}

func newUsernameClaimProfile(from *resourcesapi.UsernameClaimProfile) generated.UsernameClaimProfile {
	if from == nil {
		return generated.UsernameClaimProfile{}
	}
	return generated.UsernameClaimProfile{
		Claim:        resourcesapi.PtrOrNil(from.Claim),
		Prefix:       resourcesapi.PtrOrNil(from.Prefix),
		PrefixPolicy: resourcesapi.PtrOrNil(generated.UsernameClaimPrefixPolicy(from.PrefixPolicy)),
	}
}

func newGroupClaimProfile(from *resourcesapi.GroupClaimProfile) *generated.GroupClaimProfile {
	if from == nil {
		return nil
	}
	return &generated.GroupClaimProfile{
		Claim:  resourcesapi.PtrOrNil(from.Claim),
		Prefix: resourcesapi.PtrOrNil(from.Prefix),
	}
}

func newTokenClaimValidationRules(from []resourcesapi.TokenClaimValidationRule) []*generated.TokenClaimValidationRule {
	if from == nil {
		return nil
	}
	out := make([]*generated.TokenClaimValidationRule, 0, len(from))
	for _, rule := range from {
		out = append(out, &generated.TokenClaimValidationRule{
			Type:          resourcesapi.PtrOrNil(generated.TokenValidationRuleType(rule.Type)),
			RequiredClaim: resourcesapi.PtrOrNil(newTokenRequiredClaim(&rule.RequiredClaim)),
		})
	}
	return out
}

func newTokenRequiredClaim(from *resourcesapi.TokenRequiredClaim) generated.TokenRequiredClaim {
	if from == nil {
		return generated.TokenRequiredClaim{}
	}
	return generated.TokenRequiredClaim{
		Claim:         resourcesapi.PtrOrNil(from.Claim),
		RequiredValue: resourcesapi.PtrOrNil(from.RequiredValue),
	}
}

func (v version) NewHCPOpenShiftClusterExternalAuth(from *resourcesapi.HCPOpenShiftClusterExternalAuth) resourcesapi.VersionedHCPOpenShiftClusterExternalAuth {
	if from == nil {
		ret := &ExternalAuth{}
		SetDefaultValuesExternalAuth(ret)
		return ret
	}

	idString := ""
	if from.ID != nil {
		idString = from.ID.String()
	}

	out := &ExternalAuth{
		generated.ExternalAuth{
			ID:         resourcesapi.PtrOrNil(idString),
			Name:       resourcesapi.PtrOrNil(from.Name),
			Type:       resourcesapi.PtrOrNil(from.Type),
			SystemData: resourcesapi.PtrOrNil(newSystemData(from.SystemData)),
			Properties: &generated.ExternalAuthProperties{
				ProvisioningState: resourcesapi.PtrOrNil(generated.ExternalAuthProvisioningState(from.Properties.ProvisioningState)),
				Condition:         resourcesapi.PtrOrNil(newExternalAuthCondition(&from.Properties.Condition)),
				Issuer:            resourcesapi.PtrOrNil(newTokenIssuerProfile(&from.Properties.Issuer)),
				Claim:             resourcesapi.PtrOrNil(newExternalAuthClaimProfile(&from.Properties.Claim)),
			},
		},
	}

	for _, client := range from.Properties.Clients {
		out.Properties.Clients = append(out.Properties.Clients, &generated.ExternalAuthClientProfile{
			Component:   resourcesapi.PtrOrNil(newExternalAuthClientComponent(&client.Component)),
			ClientID:    resourcesapi.PtrOrNil(client.ClientID),
			ExtraScopes: resourcesapi.StringSliceToStringPtrSlice(client.ExtraScopes),
			Type:        resourcesapi.PtrOrNil(generated.ExternalAuthClientType(client.Type)),
		})
	}
	return out
}
