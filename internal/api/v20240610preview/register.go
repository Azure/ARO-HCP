package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
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
	validate.RegisterAlias("enum_actiontype", EnumValidateTag(generated.PossibleActionTypeValues()...))
	validate.RegisterAlias("enum_createdbytype", EnumValidateTag(generated.PossibleCreatedByTypeValues()...))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(generated.PossibleManagedServiceIdentityTypeValues()...))
	validate.RegisterAlias("enum_networktype", EnumValidateTag(generated.PossibleNetworkTypeValues()...))
	validate.RegisterAlias("enum_origin", EnumValidateTag(generated.PossibleOriginValues()...))
	validate.RegisterAlias("enum_outboundtype", EnumValidateTag(generated.PossibleOutboundTypeValues()...))
	validate.RegisterAlias("enum_provisioningstate", EnumValidateTag(generated.PossibleProvisioningStateValues()...))
	validate.RegisterAlias("enum_resourceprovisioningstate", EnumValidateTag(generated.PossibleResourceProvisioningStateValues()...))
	validate.RegisterAlias("enum_visibility", EnumValidateTag(generated.PossibleVisibilityValues()...))
}
