package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
)

type version struct{}

// String returns the api-version parameter value for this API.
func (v version) String() string {
	return "2024-06-10-preview"
}

var validate = api.NewValidator()

func EnumValidateTag[S ~string](values ...S) string {
	s := make([]string, len(values))
	for i, e := range values {
		s[i] = string(e)
	}
	return fmt.Sprintf("oneof=%s", strings.Join(s, " "))
}

func init() {
	api.Register(version{})

	// Register enum type validations
	validate.RegisterAlias("enum_actiontype", EnumValidateTag(PossibleActionTypeValues()...))
	validate.RegisterAlias("enum_createdbytype", EnumValidateTag(PossibleCreatedByTypeValues()...))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(PossibleManagedServiceIdentityTypeValues()...))
	validate.RegisterAlias("enum_networktype", EnumValidateTag(PossibleNetworkTypeValues()...))
	validate.RegisterAlias("enum_origin", EnumValidateTag(PossibleOriginValues()...))
	validate.RegisterAlias("enum_outboundtype", EnumValidateTag(PossibleOutboundTypeValues()...))
	validate.RegisterAlias("enum_provisioningstate", EnumValidateTag(PossibleProvisioningStateValues()...))
	validate.RegisterAlias("enum_resourceprovisioningstate", EnumValidateTag(PossibleResourceProvisioningStateValues()...))
	validate.RegisterAlias("enum_versions", EnumValidateTag(PossibleVersionsValues()...))
	validate.RegisterAlias("enum_visibility", EnumValidateTag(PossibleVisibilityValues()...))
}
