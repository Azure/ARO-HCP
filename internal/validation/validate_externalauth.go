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

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

func ValidateExternalAuthCreate(ctx context.Context, newObj *resourcesapi.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateExternalAuth(ctx, op, newObj, nil)
}

func ValidateExternalAuthUpdate(ctx context.Context, newObj, oldObj *resourcesapi.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return validateExternalAuth(ctx, op, newObj, oldObj)
}

var (
	toExternalAuthProxyResource = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuth) *armresourcesapi.ProxyResource {
		return &oldObj.ProxyResource
	}
	toExternalAuthProperties = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuth) *resourcesapi.HCPOpenShiftClusterExternalAuthProperties {
		return &oldObj.Properties
	}
	toExternalAuthServiceProviderProperties = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuth) *resourcesapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties {
		return &oldObj.ServiceProviderProperties
	}
)

func validateExternalAuth(ctx context.Context, op operation.Operation, newObj, oldObj *resourcesapi.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	errs := field.ErrorList{}

	//armresourcesapi.ProxyResource
	errs = append(errs, validateProxyResource(ctx, op, field.NewPath("trackedResource"), &newObj.ProxyResource, safe.Field(oldObj, toExternalAuthProxyResource))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, field.NewPath("id"), newObj.ID, nil, resourcesapi.ExternalAuthResourceType.String())...)

	//Properties HCPOpenShiftClusterExternalAuthProperties `json:"properties"`
	errs = append(errs, validateExternalAuthProperties(ctx, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, toExternalAuthProperties))...)

	//ServiceProviderProperties HCPOpenShiftClusterExternalAuthServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	errs = append(errs, validateExternalAuthServiceProviderProperties(ctx, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties, safe.Field(oldObj, toExternalAuthServiceProviderProperties))...)

	return errs
}

var (
	toProxyResourceResource = func(oldObj *armresourcesapi.ProxyResource) *armresourcesapi.Resource { return &oldObj.Resource }
)

func validateProxyResource(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *armresourcesapi.ProxyResource) field.ErrorList {
	errs := field.ErrorList{}

	//Resource
	errs = append(errs, validateResource(ctx, op, fldPath.Child("resource"), &newObj.Resource, safe.Field(oldObj, toProxyResourceResource))...)

	return errs
}

var (
	toExternalAuthPropertiesProvisioningState = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthProperties) *armresourcesapi.ProvisioningState {
		return &oldObj.ProvisioningState
	}
	toExternalAuthPropertiesCondition = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthProperties) *resourcesapi.ExternalAuthCondition {
		return &oldObj.Condition
	}
	toExternalAuthPropertiesIssuer = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthProperties) *resourcesapi.TokenIssuerProfile {
		return &oldObj.Issuer
	}
	toExternalAuthPropertiesClients = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthProperties) []resourcesapi.ExternalAuthClientProfile {
		return oldObj.Clients
	}
	toExternalAuthPropertiesClaim = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthProperties) *resourcesapi.ExternalAuthClaimProfile {
		return &oldObj.Claim
	}
)

func validateExternalAuthProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthProperties) field.ErrorList {
	errs := field.ErrorList{}

	//ProvisioningState armresourcesapi.ProvisioningState       `json:"provisioningState"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("provisioningState"), &newObj.ProvisioningState, safe.Field(oldObj, toExternalAuthPropertiesProvisioningState))...)

	//Condition         ExternalAuthCondition       `json:"condition,omitzero"`
	errs = append(errs, validateExternalAuthCondition(ctx, op, fldPath.Child("condition"), &newObj.Condition, safe.Field(oldObj, toExternalAuthPropertiesCondition))...)

	//Issuer            TokenIssuerProfile          `json:"issuer"`
	errs = append(errs, validateTokenIssuerProfile(ctx, op, fldPath.Child("issuer"), &newObj.Issuer, safe.Field(oldObj, toExternalAuthPropertiesIssuer))...)

	//Clients           []ExternalAuthClientProfile `json:"clients"`
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("clients"), newObj.Clients, safe.Field(oldObj, toExternalAuthPropertiesClients), 20)...)
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("clients"),
		newObj.Clients, safe.Field(oldObj, toExternalAuthPropertiesClients),
		nil, nil,
		validateExternalAuthClientProfile,
	)...)
	errs = append(errs, validate.Unique(
		ctx, op, fldPath.Child("clients"),
		newObj.Clients, safe.Field(oldObj, toExternalAuthPropertiesClients),
		func(lhs resourcesapi.ExternalAuthClientProfile, rhs resourcesapi.ExternalAuthClientProfile) bool {
			return lhs.Component == rhs.Component
		},
	)...)

	//Claim             ExternalAuthClaimProfile    `json:"claim"`
	errs = append(errs, validateExternalAuthClaimProfile(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toExternalAuthPropertiesClaim))...)

	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("clients"),
		newObj.Clients, safe.Field(oldObj, toExternalAuthPropertiesClients),
		nil, nil,
		func(ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue *resourcesapi.ExternalAuthClientProfile) field.ErrorList {
			for _, audience := range newObj.Issuer.Audiences {
				if audience == newValue.ClientID {
					return nil
				}
			}
			return field.ErrorList{
				field.Invalid(fldPath.Child("clientId"), newValue.ClientID, "must match an audience in issuer audiences"),
			}
		},
	)...)

	return errs
}

var (
	toExternalAuthServiceProviderClusterServiceID = func(oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties) *resourcesapi.InternalID {
		return oldObj.ClusterServiceID
	}
)

func validateExternalAuthServiceProviderProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	//ClusterServiceID  *InternalID                     `json:"clusterServiceID,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("clusterServiceID"), newObj.ClusterServiceID, safe.Field(oldObj, toExternalAuthServiceProviderClusterServiceID))...)

	return errs
}

var (
	toTokenIssuerProfileURL       = func(oldObj *resourcesapi.TokenIssuerProfile) *string { return &oldObj.URL }
	toTokenIssuerProfileAudiences = func(oldObj *resourcesapi.TokenIssuerProfile) []string { return oldObj.Audiences }
	toTokenIssuerProfileCA        = func(oldObj *resourcesapi.TokenIssuerProfile) *string { return &oldObj.CA }

	startsWithHTTPSString      = "^https://.*"
	startsWithHTTPSRegex       = regexp.MustCompile(startsWithHTTPSString)
	startsWithHTTPSErrorString = `must be https URL`
)

func validateTokenIssuerProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.TokenIssuerProfile) field.ErrorList {
	errs := field.ErrorList{}

	//URL       string   `json:"url"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toTokenIssuerProfileURL))...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toTokenIssuerProfileURL), startsWithHTTPSRegex, startsWithHTTPSErrorString)...)

	//Audiences []string `json:"audiences"`
	errs = append(errs, validate.RequiredSlice(ctx, op, fldPath.Child("audiences"), newObj.Audiences, safe.Field(oldObj, toTokenIssuerProfileAudiences))...)
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

	//CA        string   `json:"ca"`
	errs = append(errs, ValidatePEM(ctx, op, fldPath.Child("ca"), &newObj.CA, safe.Field(oldObj, toTokenIssuerProfileCA))...)

	return errs
}

var (
	toExternalAuthClientProfileComponent = func(oldObj *resourcesapi.ExternalAuthClientProfile) *resourcesapi.ExternalAuthClientComponentProfile {
		return &oldObj.Component
	}
	toExternalAuthClientProfileClientID = func(oldObj *resourcesapi.ExternalAuthClientProfile) *string { return &oldObj.ClientID }
	toExternalAuthClientProfileType     = func(oldObj *resourcesapi.ExternalAuthClientProfile) *resourcesapi.ExternalAuthClientType {
		return &oldObj.Type
	}
)

func validateExternalAuthClientProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.ExternalAuthClientProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Component   ExternalAuthClientComponentProfile `json:"component"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("component"), &newObj.Component, safe.Field(oldObj, toExternalAuthClientProfileComponent))...)
	errs = append(errs, validateExternalAuthClientComponentProfile(ctx, op, fldPath.Child("component"), &newObj.Component, safe.Field(oldObj, toExternalAuthClientProfileComponent))...)

	//ClientID    string                             `json:"clientId"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("clientId"), &newObj.ClientID, safe.Field(oldObj, toExternalAuthClientProfileClientID))...)

	//ExtraScopes []string                           `json:"extraScopes"`

	//Type        ExternalAuthClientType             `json:"type"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toExternalAuthClientProfileType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toExternalAuthClientProfileType), resourcesapi.ValidExternalAuthClientTypes, nil)...)

	return errs
}

var (
	toExternalAuthClientComponentProfileName                = func(oldObj *resourcesapi.ExternalAuthClientComponentProfile) *string { return &oldObj.Name }
	toExternalAuthClientComponentProfileAuthClientNamespace = func(oldObj *resourcesapi.ExternalAuthClientComponentProfile) *string {
		return &oldObj.AuthClientNamespace
	}
)

func validateExternalAuthClientComponentProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.ExternalAuthClientComponentProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Name                string `json:"name"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toExternalAuthClientComponentProfileName))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toExternalAuthClientComponentProfileName), 256)...)

	//AuthClientNamespace string `json:"authClientNamespace"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("authClientNamespace"), &newObj.AuthClientNamespace, safe.Field(oldObj, toExternalAuthClientComponentProfileAuthClientNamespace))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("authClientNamespace"), &newObj.AuthClientNamespace, safe.Field(oldObj, toExternalAuthClientComponentProfileAuthClientNamespace), 63)...)

	return errs
}

var (
	toExternalAuthClaimProfileMappings = func(oldObj *resourcesapi.ExternalAuthClaimProfile) *resourcesapi.TokenClaimMappingsProfile {
		return &oldObj.Mappings
	}
	toExternalAuthClaimProfileValidationRules = func(oldObj *resourcesapi.ExternalAuthClaimProfile) []resourcesapi.TokenClaimValidationRule {
		return oldObj.ValidationRules
	}
)

func validateExternalAuthClaimProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.ExternalAuthClaimProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Mappings        TokenClaimMappingsProfile  `json:"mappings"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("mappings"), &newObj.Mappings, safe.Field(oldObj, toExternalAuthClaimProfileMappings))...)
	errs = append(errs, validateTokenClaimMappingsProfile(ctx, op, fldPath.Child("mappings"), &newObj.Mappings, safe.Field(oldObj, toExternalAuthClaimProfileMappings))...)

	//ValidationRules []TokenClaimValidationRule `json:"validationRules"`
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("validationRules"),
		newObj.ValidationRules, safe.Field(oldObj, toExternalAuthClaimProfileValidationRules),
		nil, nil,
		validateTokenClaimValidationRule,
	)...)

	return errs
}

var (
	toTokenClaimMappingsProfileUsername = func(oldObj *resourcesapi.TokenClaimMappingsProfile) *resourcesapi.UsernameClaimProfile {
		return &oldObj.Username
	}
	toTokenClaimMappingsProfileGroups = func(oldObj *resourcesapi.TokenClaimMappingsProfile) *resourcesapi.GroupClaimProfile {
		return oldObj.Groups
	}
)

func validateTokenClaimMappingsProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.TokenClaimMappingsProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Username UsernameClaimProfile `json:"username"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("username"), &newObj.Username, safe.Field(oldObj, toTokenClaimMappingsProfileUsername))...)
	errs = append(errs, validateUsernameClaimProfile(ctx, op, fldPath.Child("username"), &newObj.Username, safe.Field(oldObj, toTokenClaimMappingsProfileUsername))...)

	//Groups   *GroupClaimProfile   `json:"groups"`
	errs = append(errs, validateGroupClaimProfile(ctx, op, fldPath.Child("groups"), newObj.Groups, safe.Field(oldObj, toTokenClaimMappingsProfileGroups))...)

	return errs
}

var (
	toUsernameClaimProfileClaim        = func(oldObj *resourcesapi.UsernameClaimProfile) *string { return &oldObj.Claim }
	toUsernameClaimProfilePrefixPolicy = func(oldObj *resourcesapi.UsernameClaimProfile) *resourcesapi.UsernameClaimPrefixPolicy {
		return &oldObj.PrefixPolicy
	}
)

func validateUsernameClaimProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.UsernameClaimProfile) field.ErrorList {
	errs := field.ErrorList{}

	//Claim        string                    `json:"claim"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toUsernameClaimProfileClaim))...)

	//Prefix       string                    `json:"prefix"`

	//PrefixPolicy UsernameClaimPrefixPolicy `json:"prefixPolicy"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("prefixPolicy"), &newObj.PrefixPolicy, safe.Field(oldObj, toUsernameClaimProfilePrefixPolicy), resourcesapi.ValidUsernameClaimPrefixPolicies, nil)...)
	union := validate.NewDiscriminatedUnionMembership("prefixPolicy", validate.NewDiscriminatedUnionMember("prefix", string(resourcesapi.UsernameClaimPrefixPolicyPrefix)))
	discriminatorExtractor := func(obj *resourcesapi.UsernameClaimProfile) resourcesapi.UsernameClaimPrefixPolicy {
		return obj.PrefixPolicy
	}
	isPrefixSetFn := func(obj *resourcesapi.UsernameClaimProfile) bool {
		return len(obj.Prefix) > 0
	}
	// this verifies that Prefix is set iff prefixPolicy==Prefix
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isPrefixSetFn)...)

	return errs
}

var (
	toGroupClaimProfileClaim = func(oldObj *resourcesapi.GroupClaimProfile) *string { return &oldObj.Claim }
)

func validateGroupClaimProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.GroupClaimProfile) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//Claim  string `json:"claim"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toGroupClaimProfileClaim))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toGroupClaimProfileClaim), 256)...)

	//Prefix string `json:"prefix"`

	return errs
}

var (
	toTokenClaimValidationRuleType = func(oldObj *resourcesapi.TokenClaimValidationRule) *resourcesapi.TokenValidationRuleType {
		return &oldObj.Type
	}
	toTokenClaimValidationRuleRequiredClaim = func(oldObj *resourcesapi.TokenClaimValidationRule) *resourcesapi.TokenRequiredClaim {
		return &oldObj.RequiredClaim
	}
)

func validateTokenClaimValidationRule(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.TokenClaimValidationRule) field.ErrorList {
	errs := field.ErrorList{}

	//Type          TokenValidationRuleType `json:"type"`
	// TODO discriminated unions should be pointers
	//RequiredClaim TokenRequiredClaim      `json:"requiredClaim"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toTokenClaimValidationRuleType), resourcesapi.ValidTokenValidationRuleTypes, nil)...)
	union := validate.NewDiscriminatedUnionMembership("type", validate.NewDiscriminatedUnionMember("requiredClaim", string(resourcesapi.TokenValidationRuleTypeRequiredClaim)))
	discriminatorExtractor := func(obj *resourcesapi.TokenClaimValidationRule) resourcesapi.TokenValidationRuleType {
		return obj.Type
	}
	isRequiredClaimSetFn := func(obj *resourcesapi.TokenClaimValidationRule) bool {
		return !reflect.DeepEqual(obj.RequiredClaim, resourcesapi.TokenRequiredClaim{})
	}
	// this verifies that RequiredClaim is set iff Type==RequiredClaim
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isRequiredClaimSetFn)...)

	errs = append(errs, validateTokenRequiredClaim(ctx, op, fldPath.Child("requiredClaim"), &newObj.RequiredClaim, safe.Field(oldObj, toTokenClaimValidationRuleRequiredClaim))...)

	return errs
}

var (
	toTokenRequiredClaimClaim         = func(oldObj *resourcesapi.TokenRequiredClaim) *string { return &oldObj.Claim }
	toTokenRequiredClaimRequiredValue = func(oldObj *resourcesapi.TokenRequiredClaim) *string { return &oldObj.RequiredValue }
)

func validateTokenRequiredClaim(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.TokenRequiredClaim) field.ErrorList {
	errs := field.ErrorList{}

	//Claim         string `json:"claim"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("claim"), &newObj.Claim, safe.Field(oldObj, toTokenRequiredClaimClaim))...)

	//RequiredValue string `json:"requiredValue"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("requiredValue"), &newObj.RequiredValue, safe.Field(oldObj, toTokenRequiredClaimRequiredValue))...)

	return errs
}

var (
	toExternalAuthConditionType = func(oldObj *resourcesapi.ExternalAuthCondition) *resourcesapi.ExternalAuthConditionType {
		return &oldObj.Type
	}
	toExternalAuthConditionStatus = func(oldObj *resourcesapi.ExternalAuthCondition) *resourcesapi.ConditionStatusType {
		return &oldObj.Status
	}
)

func validateExternalAuthCondition(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *resourcesapi.ExternalAuthCondition) field.ErrorList {
	if newObj == nil || reflect.DeepEqual(*newObj, resourcesapi.ExternalAuthCondition{}) {
		return nil
	}

	errs := field.ErrorList{}

	//Type               ExternalAuthConditionType `json:"type"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toExternalAuthConditionType), resourcesapi.ValidExternalAuthConditionTypes, nil)...)

	//Status             ConditionStatusType       `json:"status"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("status"), &newObj.Status, safe.Field(oldObj, toExternalAuthConditionStatus), resourcesapi.ValidConditionStatusTypes, nil)...)

	//LastTransitionTime time.Time                 `json:"lastTransitionTime"`
	//Reason             string                    `json:"reason"`
	//Message            string                    `json:"message"`

	return errs
}
