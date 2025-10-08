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

package validation

import (
	"context"
	"reflect"
	"regexp"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func ValidateExternalAuthCreate(ctx context.Context, newObj *api.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateExternalAuth(ctx, op, newObj, nil)
}

func ValidateExternalAuthUpdate(ctx context.Context, newObj, oldObj *api.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return validateExternalAuth(ctx, op, newObj, oldObj)
}

var (
	toExternalAuthProxyResource = func(oldObj *api.HCPOpenShiftClusterExternalAuth) *arm.ProxyResource { return &oldObj.ProxyResource }
	toExternalAuthProperties    = func(oldObj *api.HCPOpenShiftClusterExternalAuth) *api.HCPOpenShiftClusterExternalAuthProperties {
		return &oldObj.Properties
	}
)

func validateExternalAuth(ctx context.Context, op operation.Operation, newObj, oldObj *api.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	errs := field.ErrorList{}

	//arm.ProxyResource
	errs = append(errs, validateProxyResource(ctx, op, field.NewPath("trackedResource"), &newObj.ProxyResource, safe.Field(oldObj, toExternalAuthProxyResource))...)

	//Properties HCPOpenShiftClusterExternalAuthProperties `json:"properties" validate:"required"`
	errs = append(errs, validateExternalAuthProperties(ctx, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, toExternalAuthProperties))...)

	return errs
}

var (
	toProxyResourceResource = func(oldObj *arm.ProxyResource) *arm.Resource { return &oldObj.Resource }
)

func validateProxyResource(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *arm.ProxyResource) field.ErrorList {
	errs := field.ErrorList{}

	//Resource
	errs = append(errs, validateResource(ctx, op, fldPath.Child("resource"), &newObj.Resource, safe.Field(oldObj, toProxyResourceResource))...)

	return errs
}

var (
	toExternalAuthPropertiesProvisioningState = func(oldObj *api.HCPOpenShiftClusterExternalAuthProperties) *arm.ProvisioningState {
		return &oldObj.ProvisioningState
	}
	toExternalAuthPropertiesCondition = func(oldObj *api.HCPOpenShiftClusterExternalAuthProperties) *api.ExternalAuthCondition {
		return &oldObj.Condition
	}
	toExternalAuthPropertiesIssuer = func(oldObj *api.HCPOpenShiftClusterExternalAuthProperties) *api.TokenIssuerProfile {
		return &oldObj.Issuer
	}
	toExternalAuthPropertiesClients = func(oldObj *api.HCPOpenShiftClusterExternalAuthProperties) []api.ExternalAuthClientProfile {
		return oldObj.Clients
	}
	toExternalAuthPropertiesClaim = func(oldObj *api.HCPOpenShiftClusterExternalAuthProperties) *api.ExternalAuthClaimProfile {
		return &oldObj.Claim
	}
)

func validateExternalAuthProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterExternalAuthProperties) field.ErrorList {
	errs := field.ErrorList{}

	//ProvisioningState arm.ProvisioningState       `json:"provisioningState"       visibility:"read"                     validate:"omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("provisioningState"), &newObj.ProvisioningState, safe.Field(oldObj, toExternalAuthPropertiesProvisioningState))...)

	//Condition         ExternalAuthCondition       `json:"condition,omitzero"      visibility:"read"                     validate:"omitempty"`
	errs = append(errs, validateExternalAuthCondition(ctx, op, fldPath.Child("condition"), &newObj.Condition, safe.Field(oldObj, toExternalAuthPropertiesCondition))...)

	//Issuer            TokenIssuerProfile          `json:"issuer"                  visibility:"read create update"       validate:"required"`
	errs = append(errs, validateTokenIssuerProfile(ctx, op, fldPath.Child("issuer"), &newObj.Issuer, safe.Field(oldObj, toExternalAuthPropertiesIssuer))...)

	//Clients           []ExternalAuthClientProfile `json:"clients"                 visibility:"read create update"       validate:"omitempty,max=20,dive"`
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("clients"), newObj.Clients, safe.Field(oldObj, toExternalAuthPropertiesClients), 20)...)
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("clients"),
		newObj.Clients, safe.Field(oldObj, toExternalAuthPropertiesClients),
		nil, nil,
		validateExternalAuthClientProfile,
	)...)

	//Claim             ExternalAuthClaimProfile    `json:"claim"                   visibility:"read create update"       validate:"required"`
	errs = append(errs, validateExternalAuthClaimProfile(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toExternalAuthPropertiesClaim))...)

	return errs
}

var (
	toTokenIssuerProfileURL       = func(oldObj *api.TokenIssuerProfile) *string { return &oldObj.URL }
	toTokenIssuerProfileAudiences = func(oldObj *api.TokenIssuerProfile) []string { return oldObj.Audiences }
	toTokenIssuerProfileCA        = func(oldObj *api.TokenIssuerProfile) *string { return &oldObj.CA }

	startsWithHTTPSString      = "^https://.*"
	startsWithHTTPSRegex       = regexp.MustCompile(startsWithHTTPSString)
	startsWithHTTPSErrorString = `must be https URL`
)

func validateTokenIssuerProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.TokenIssuerProfile) field.ErrorList {
	errs := field.ErrorList{}

	// TODO why didn't we make this a URL?
	//URL       string   `json:"url"       validate:"required,url,startswith=https://"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toTokenIssuerProfileURL))...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toTokenIssuerProfileURL), startsWithHTTPSRegex, startsWithHTTPSErrorString)...)

	//Audiences []string `json:"audiences" validate:"required,max=10"`
	errs = append(errs, MinItems(ctx, op, fldPath.Child("audiences"), newObj.Audiences, safe.Field(oldObj, toTokenIssuerProfileAudiences), 1)...)
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("audiences"), newObj.Audiences, safe.Field(oldObj, toTokenIssuerProfileAudiences), 10)...)
	// TODO I bet these were forgotten
	//errs = append(errs, validate.EachSliceVal(
	//	ctx, op, fldPath.Child("audiences"),
	//	newObj.Audiences, safe.Field(oldObj, toTokenIssuerProfileAudiences),
	//	nil, nil,
	//	validate.RequiredValue,
	//)...)
	//errs = append(errs, validate.EachSliceVal(
	//	ctx, op, fldPath.Child("audiences"),
	//	newObj.Audiences, safe.Field(oldObj, toTokenIssuerProfileAudiences),
	//	nil, nil,
	//	NoExtraWhitespace,
	//)...)

	//CA        string   `json:"ca"        validate:"omitempty,pem_certificates"`
	errs = append(errs, ValidatePEM(ctx, op, fldPath.Child("ca"), &newObj.CA, safe.Field(oldObj, toTokenIssuerProfileCA))...)

	return errs
}

var (
	toExternalAuthClientProfileComponent = func(oldObj *api.ExternalAuthClientProfile) *api.ExternalAuthClientComponentProfile {
		return &oldObj.Component
	}
	toExternalAuthClientProfileClientID = func(oldObj *api.ExternalAuthClientProfile) *string { return &oldObj.ClientID }
	toExternalAuthClientProfileType     = func(oldObj *api.ExternalAuthClientProfile) *api.ExternalAuthClientType { return &oldObj.Type }
)

func validateExternalAuthClientProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ExternalAuthClientProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Component   ExternalAuthClientComponentProfile `json:"component"   validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("component"), &newObj.Component, safe.Field(oldObj, toExternalAuthClientProfileComponent))...)
	errs = append(errs, validateExternalAuthClientComponentProfile(ctx, op, fldPath.Child("component"), &newObj.Component, safe.Field(oldObj, toExternalAuthClientProfileComponent))...)

	//ClientID    string                             `json:"clientId"    validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("clientId"), &newObj.ClientID, safe.Field(oldObj, toExternalAuthClientProfileClientID))...)

	//ExtraScopes []string                           `json:"extraScopes" validate:"omitempty"`

	//Type        ExternalAuthClientType             `json:"type"        validate:"required,enum_externalauthclienttype"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toExternalAuthClientProfileType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toExternalAuthClientProfileType), api.ValidExternalAuthClientTypes)...)

	return errs
}

var (
	toExternalAuthClientComponentProfileName                = func(oldObj *api.ExternalAuthClientComponentProfile) *string { return &oldObj.Name }
	toExternalAuthClientComponentProfileAuthClientNamespace = func(oldObj *api.ExternalAuthClientComponentProfile) *string { return &oldObj.AuthClientNamespace }
)

func validateExternalAuthClientComponentProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ExternalAuthClientComponentProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Name                string `json:"name"                validate:"required,max=256"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toExternalAuthClientComponentProfileName))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toExternalAuthClientComponentProfileName), 256)...)

	//AuthClientNamespace string `json:"authClientNamespace" validate:"required,max=63"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("authClientNamespace"), &newObj.AuthClientNamespace, safe.Field(oldObj, toExternalAuthClientComponentProfileAuthClientNamespace))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("authClientNamespace"), &newObj.AuthClientNamespace, safe.Field(oldObj, toExternalAuthClientComponentProfileAuthClientNamespace), 63)...)

	return errs
}

var (
	toExternalAuthClaimProfileMappings        = func(oldObj *api.ExternalAuthClaimProfile) *api.TokenClaimMappingsProfile { return &oldObj.Mappings }
	toExternalAuthClaimProfileValidationRules = func(oldObj *api.ExternalAuthClaimProfile) []api.TokenClaimValidationRule {
		return oldObj.ValidationRules
	}
)

func validateExternalAuthClaimProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ExternalAuthClaimProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Mappings        TokenClaimMappingsProfile  `json:"mappings"        validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("mappings"), &newObj.Mappings, safe.Field(oldObj, toExternalAuthClaimProfileMappings))...)
	errs = append(errs, validateTokenClaimMappingsProfile(ctx, op, fldPath.Child("mappings"), &newObj.Mappings, safe.Field(oldObj, toExternalAuthClaimProfileMappings))...)

	//ValidationRules []TokenClaimValidationRule `json:"validationRules" validate:"omitempty,dive"`
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("validationRules"),
		newObj.ValidationRules, safe.Field(oldObj, toExternalAuthClaimProfileValidationRules),
		nil, nil,
		validateTokenClaimValidationRule,
	)...)

	return errs
}

var (
	toTokenClaimMappingsProfileUsername = func(oldObj *api.TokenClaimMappingsProfile) *api.UsernameClaimProfile { return &oldObj.Username }
	toTokenClaimMappingsProfileGroups   = func(oldObj *api.TokenClaimMappingsProfile) *api.GroupClaimProfile { return oldObj.Groups }
)

func validateTokenClaimMappingsProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.TokenClaimMappingsProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Username UsernameClaimProfile `json:"username" validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("username"), &newObj.Username, safe.Field(oldObj, toTokenClaimMappingsProfileUsername))...)
	errs = append(errs, validateUsernameClaimProfile(ctx, op, fldPath.Child("username"), &newObj.Username, safe.Field(oldObj, toTokenClaimMappingsProfileUsername))...)

	//Groups   *GroupClaimProfile   `json:"groups"   validate:"omitempty"`
	errs = append(errs, validateGroupClaimProfile(ctx, op, fldPath.Child("groups"), newObj.Groups, safe.Field(oldObj, toTokenClaimMappingsProfileGroups))...)

	return errs
}

var (
	toUsernameClaimProfileClaim        = func(oldObj *api.UsernameClaimProfile) *string { return &oldObj.Claim }
	toUsernameClaimProfilePrefixPolicy = func(oldObj *api.UsernameClaimProfile) *api.UsernameClaimPrefixPolicy { return &oldObj.PrefixPolicy }
)

func validateUsernameClaimProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.UsernameClaimProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Claim        string                    `json:"claim"        validate:"required,max=256"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toUsernameClaimProfileClaim))...)

	//Prefix       string                    `json:"prefix"       validate:"required_if=PrefixPolicy Prefix,excluded_unless=PrefixPolicy Prefix"`

	//PrefixPolicy UsernameClaimPrefixPolicy `json:"prefixPolicy" validate:"enum_usernameclaimprefixpolicy"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("prefixPolicy"), &newObj.PrefixPolicy, safe.Field(oldObj, toUsernameClaimProfilePrefixPolicy), api.ValidUsernameClaimPrefixPolicies)...)
	union := validate.NewDiscriminatedUnionMembership("prefixPolicy", [2]string{"prefix", string(api.UsernameClaimPrefixPolicyPrefix)})
	discriminatorExtractor := func(obj *api.UsernameClaimProfile) api.UsernameClaimPrefixPolicy {
		return obj.PrefixPolicy
	}
	isPrefixSetFn := func(obj *api.UsernameClaimProfile) bool {
		return len(obj.Prefix) > 0
	}
	// this verifies that Prefix is set iff prefixPolicy==Prefix
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isPrefixSetFn)...)

	return errs
}

var (
	toGroupClaimProfileClaim = func(oldObj *api.GroupClaimProfile) *string { return &oldObj.Claim }
)

func validateGroupClaimProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.GroupClaimProfile) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//Claim  string `json:"claim"  validate:"required,max=256"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toGroupClaimProfileClaim))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toGroupClaimProfileClaim), 256)...)

	//Prefix string `json:"prefix" validate:"omitempty"`

	return errs
}

var (
	toTokenClaimValidationRuleType          = func(oldObj *api.TokenClaimValidationRule) *api.TokenValidationRuleType { return &oldObj.Type }
	toTokenClaimValidationRuleRequiredClaim = func(oldObj *api.TokenClaimValidationRule) *api.TokenRequiredClaim { return &oldObj.RequiredClaim }
)

func validateTokenClaimValidationRule(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.TokenClaimValidationRule) field.ErrorList {
	errs := field.ErrorList{}

	//Type          TokenValidationRuleType `json:"type"          validate:"required,enum_tokenvalidationruletyperequiredclaim"`
	// TODO discriminated unions should be pointers
	//RequiredClaim TokenRequiredClaim      `json:"requiredClaim" validate:"omitempty"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toTokenClaimValidationRuleType), api.ValidTokenValidationRuleTypes)...)
	union := validate.NewDiscriminatedUnionMembership("type", [2]string{"requiredClaim", string(api.TokenValidationRuleTypeRequiredClaim)})
	discriminatorExtractor := func(obj *api.TokenClaimValidationRule) api.TokenValidationRuleType {
		return obj.Type
	}
	isRequiredClaimSetFn := func(obj *api.TokenClaimValidationRule) bool {
		return !reflect.DeepEqual(obj.RequiredClaim, api.TokenRequiredClaim{})
	}
	// this verifies that RequiredClaim is set iff Type==RequiredClaim
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isRequiredClaimSetFn)...)

	errs = append(errs, validateTokenRequiredClaim(ctx, op, fldPath.Child("requiredClaim"), &newObj.RequiredClaim, safe.Field(oldObj, toTokenClaimValidationRuleRequiredClaim))...)

	return errs
}

var (
	toTokenRequiredClaimClaim         = func(oldObj *api.TokenRequiredClaim) *string { return &oldObj.Claim }
	toTokenRequiredClaimRequiredValue = func(oldObj *api.TokenRequiredClaim) *string { return &oldObj.RequiredValue }
)

func validateTokenRequiredClaim(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.TokenRequiredClaim) field.ErrorList {
	errs := field.ErrorList{}

	//Claim         string `json:"claim"         validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toTokenRequiredClaimClaim))...)

	//RequiredValue string `json:"requiredValue" validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("requiredValue"), &newObj.RequiredValue, safe.Field(oldObj, toTokenRequiredClaimRequiredValue))...)

	return errs
}

var (
	toExternalAuthConditionType   = func(oldObj *api.ExternalAuthCondition) *api.ExternalAuthConditionType { return &oldObj.Type }
	toExternalAuthConditionStatus = func(oldObj *api.ExternalAuthCondition) *api.ConditionStatusType { return &oldObj.Status }
)

func validateExternalAuthCondition(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ExternalAuthCondition) field.ErrorList {
	if newObj == nil || reflect.DeepEqual(*newObj, api.ExternalAuthCondition{}) {
		return nil
	}

	errs := field.ErrorList{}

	//Type               ExternalAuthConditionType `json:"type"               validate:"enum_externalauthconditiontype"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toExternalAuthConditionType), api.ValidExternalAuthConditionTypes)...)

	//Status             ConditionStatusType       `json:"status"             validate:"enum_externalauthconditionstatustype"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("status"), &newObj.Status, safe.Field(oldObj, toExternalAuthConditionStatus), api.ValidConditionStatusTypes)...)

	//LastTransitionTime time.Time                 `json:"lastTransitionTime"`
	//Reason             string                    `json:"reason"`
	//Message            string                    `json:"message"`

	return errs
}
