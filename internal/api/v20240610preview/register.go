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

var (
	validate            = api.NewValidator()
	clusterStructTagMap = api.NewStructTagMap[api.HCPOpenShiftCluster]()
)

func EnumValidateTag[S ~string](values ...S) string {
	s := make([]string, len(values))
	for i, e := range values {
		s[i] = string(e)
	}
	return fmt.Sprintf("oneof=%s", strings.Join(s, " "))
}

func init() {
	// NOTE: If future versions of the API expand field visibility, such as
	//       a field with @visibility("read","create") becoming updatable,
	//       then earlier versions of the API will need to override their
	//       StructTagMap to maintain the original visibility flags. This
	//       is where such overrides should happen, along with a comment
	//       about what changed and when. For example:
	//
	//       // This field became updatable in version YYYY-MM-DD.
	//       clusterStructTagMap["Properties.Spec.FieldName"] = reflect.StructTag("visibility:\"read create\"")
	//

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
