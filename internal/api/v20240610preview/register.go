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
	"github.com/go-playground/validator/v10"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type version struct{}

// String returns the api-version parameter value for this API.
func (v version) String() string {
	return "2024-06-10-preview"
}

// GetValidator returns the validator.Validate instance configured
// specifically for this API version.
func (v version) GetValidator() *validator.Validate {
	return validate
}

var (
	versionedInterface        = version{}
	validate                  = api.NewValidator()
	clusterVisibilityMap      = api.NewVisibilityMap[api.HCPOpenShiftCluster]()
	nodePoolVisibilityMap     = api.NewVisibilityMap[api.HCPOpenShiftClusterNodePool]()
	externalAuthVisibilityMap = api.NewVisibilityMap[api.HCPOpenShiftClusterExternalAuth]()
)

func init() {
	// NOTE: If future versions of the API expand field visibility, such as
	//       a field with @visibility("read","create") becoming updatable,
	//       then earlier versions of the API will need to override their
	//       VisibilityMap to maintain the original visibility flags. This
	//       is where such overrides should happen, along with a comment
	//       about what changed and when. For example:
	//
	//       // This field became updatable in version YYYY-MM-DD.
	//       clusterVisibilityMap["Properties.FieldName"] = api.VisibilityRead | api.VisibilityCreate
	//

	// We normalize node pool labels to a map, so there is
	// no normalized Label model with Key and Value fields.
	nodePoolVisibilityMap["Properties.Labels.Key"] = nodePoolVisibilityMap["Properties.Labels"] & api.VisibilityDefault
	nodePoolVisibilityMap["Properties.Labels.Value"] = nodePoolVisibilityMap["Properties.Labels"] & api.VisibilityDefault

	api.Register(versionedInterface)

	// Register enum type validations
	validate.RegisterAlias("enum_actiontype", api.EnumValidateTag(generated.PossibleActionTypeValues()...))
	validate.RegisterAlias("enum_clusterimageregistryprofilestate", api.EnumValidateTag(generated.PossibleClusterImageRegistryProfileStateValues()...))
	validate.RegisterAlias("enum_createdbytype", api.EnumValidateTag(generated.PossibleCreatedByTypeValues()...))
	validate.RegisterAlias("enum_customermanagedencryptiontype", api.EnumValidateTag(generated.PossibleCustomerManagedEncryptionTypeValues()...))
	validate.RegisterAlias("enum_diskstorageaccounttype", api.EnumValidateTag(generated.PossibleDiskStorageAccountTypeValues()...))
	validate.RegisterAlias("enum_effect", api.EnumValidateTag(generated.PossibleEffectValues()...))
	validate.RegisterAlias("enum_etcddataencryptionkeymanagementmodetype", api.EnumValidateTag(generated.PossibleEtcdDataEncryptionKeyManagementModeTypeValues()...))
	validate.RegisterAlias("enum_externalauthclienttype", api.EnumValidateTag(generated.PossibleExternalAuthClientTypeValues()...))
	validate.RegisterAlias("enum_externalauthconditionstatustype", api.EnumValidateTag(generated.PossibleStatusTypeValues()...))
	validate.RegisterAlias("enum_externalauthconditiontype", api.EnumValidateTag(generated.PossibleExternalAuthConditionTypeValues()...))
	validate.RegisterAlias("enum_managedserviceidentitytype", api.EnumValidateTag(generated.PossibleManagedServiceIdentityTypeValues()...))
	validate.RegisterAlias("enum_networktype", api.EnumValidateTag(generated.PossibleNetworkTypeValues()...))
	validate.RegisterAlias("enum_origin", api.EnumValidateTag(generated.PossibleOriginValues()...))
	validate.RegisterAlias("enum_outboundtype", api.EnumValidateTag(generated.PossibleOutboundTypeValues()...))
	validate.RegisterAlias("enum_provisioningstate", api.EnumValidateTag(generated.PossibleProvisioningStateValues()...))
	validate.RegisterAlias("enum_tokenvalidationruletyperequiredclaim", api.EnumValidateTag(generated.PossibleTokenValidationRuleTypeValues()...))
	validate.RegisterAlias("enum_usernameclaimprefixpolicy", api.EnumValidateTag(generated.PossibleUsernameClaimPrefixPolicyValues()...))
	validate.RegisterAlias("enum_visibility", api.EnumValidateTag(generated.PossibleVisibilityValues()...))
}
