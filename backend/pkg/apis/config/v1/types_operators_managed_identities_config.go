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

package v1

import (
	"context"
	"regexp"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azurevalidation "github.com/Azure/ARO-HCP/backend/pkg/azure/validation"
	"github.com/Azure/ARO-HCP/internal/validation"
)

var (
	// ocpMaxMinVersionRegex is a regular expression to validate the OpenShift version.
	// The format is <number>.<number>, e.g. 4.19.
	// TODO do we want to define MinOpenShiftVersion and MaxOpenShiftVersion as strings that comply with this regex,
	// or do we want them as semver.Version objects? the restriction is that it's always major.minor and nothing else.
	ocpMaxMinVersionRegex = regexp.MustCompile(`^[0-9]{1,}\.[0-9]{1,}$`)
)

// OperatorsManagedIdentitiesConfig represents the managed identities
// configuration associated with the Cluster's control plane and data plane operators.
// This configuration contains the control plane and data plane operator identities
// that are recognized by the service.
// We commonly refer to managed identities whose associated operator name is not defined in this configuration
// as unknown managed identities.
// We commonly refer to managed identities whose associated operator name is defined in this configuration but
// it is not defined within the minOpenShiftVersion and maxOpenShiftVersion constraints defined in it
// as unsupported managed identities.
type OperatorsManagedIdentitiesConfig struct {
	// ControlPlaneOperatorsIdentities is a map of control plane operator identities recognized by the service.
	// The key of the map is the name of the control plane operator.
	// TODO do we want the map values to be values or pointers?
	ControlPlaneOperatorsIdentities map[string]ControlPlaneOperatorIdentity `json:"controlPlaneOperatorsIdentities"`
	// DataPlaneOperatorsIdentities is a map of data plane operator identities recognized by the service.
	// The key of the map is the name of the data plane operator.
	DataPlaneOperatorsIdentities map[string]DataPlaneOperatorIdentity `json:"dataPlaneOperatorsIdentities"`
}

// BaseOperatorIdentity represents the base configuration for an operator.
type BaseOperatorIdentity struct {
	// MinOpenShiftVersion is the minimum OpenShift version supported by the operator.
	// The format is <number>.<number>, e.g. 4.19.
	// Not specifying it indicates that the operator is supported for all OpenShift versions,
	// or up to MaxOpenShiftVersion if MaxOpenShiftVersion is specified.
	MinOpenShiftVersion string `json:"minOpenShiftVersion"`
	// MaxOpenShiftVersion is the maximum OpenShift version supported by the operator.
	// The format is <number>.<number>, e.g. 4.19.
	// Not specifying it indicates that the operator is supported in all OpenShift versions,
	// starting from minOpenShiftVersion if minOpenShiftVersion is specified.
	MaxOpenShiftVersion string `json:"maxOpenShiftVersion"`
	// RoleDefinitions is a list of Azure Role Definitions needed by the operator.
	// The list cannot be empty and it must have a length of at least one.
	// TODO do we want the list values to be values or pointers?
	RoleDefinitions []RoleDefinition `json:"roleDefinitions"`
	// Requirement indicates the requirement for the operator identity for a successful installation
	// and/or update of a Cluster (within the minOpenShiftVersion and maxOpenShiftVersion constraints).
	// The possible values are:
	// - "always": the operator identity is always required
	// - "onEnablement": the operator identity is required only when a functionality that leverages the operator is enabled
	Requirement IdentityRequirement `json:"requirement"`
}

// IdentityRequirement represents the requirement for an operator identity.
type IdentityRequirement string

const (
	// AlwaysRequiredIdentityRequirement indicates that the operator identity is always required.
	AlwaysRequiredIdentityRequirement IdentityRequirement = "always"
	// RequiredOnEnablementIdentityRequirement indicates that the operator identity is required only when a functionality that leverages the operator is enabled.
	RequiredOnEnablementIdentityRequirement IdentityRequirement = "onEnablement"
)

// ControlPlaneOperatorIdentity represents the configuration for a control plane operator.
type ControlPlaneOperatorIdentity struct {
	// BaseOperatorIdentity is the base configuration for the control plane operator.
	BaseOperatorIdentity `json:",inline"`
}

// DataPlaneOperatorIdentity represents the configuration for a data plane operator.
type DataPlaneOperatorIdentity struct {
	BaseOperatorIdentity `json:",inline"`
	// KubernetesServiceAccounts is a list of Kubernetes Service Accounts needed by the operator.
	// There must be at least a single Kubernetes Service Account specified. This
	// information is used to federate an Azure Managed Identity to a K8s Subject.
	// Duplicate name:namespace entries are not allowed.
	KubernetesServiceAccounts []KubernetesServiceAccount `json:"k8sServiceAccounts"`
}

// KubernetesServiceAccount represents a Kubernetes Service Account.
type KubernetesServiceAccount struct {
	// The name of the Kubernetes Service Account
	Name string `json:"name"`
	// The namespace of the Kubernetes Service Account
	Namespace string `json:"namespace"`
}

// RoleDefinition represents an Azure Role Definition.
type RoleDefinition struct {
	// Name is the name of the Role defined in ResourceID. It is purely a friendly/descriptive name of the Azure Role Definition
	Name string `json:"name"`
	// The resource ID of the Azure Role Definition
	ResourceID *azcorearm.ResourceID `json:"resourceID"`
}

func (c OperatorsManagedIdentitiesConfig) Validate(ctx context.Context, op operation.Operation) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, c.validateControlPlaneOperatorsIdentities(ctx, op, field.NewPath("controlPlaneOperatorsIdentities"))...)
	errs = append(errs, c.validateDataPlaneOperatorsIdentities(ctx, op, field.NewPath("dataPlaneOperatorsIdentities"))...)

	return errs
}

func (c OperatorsManagedIdentitiesConfig) validateControlPlaneOperatorsIdentities(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	for operatorName, controlPlaneOperatorIdentity := range c.ControlPlaneOperatorsIdentities {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Key(operatorName), &operatorName, nil)...)
		errs = append(errs, controlPlaneOperatorIdentity.validate(ctx, op, fldPath.Key(operatorName))...)
	}

	return errs
}

func (c OperatorsManagedIdentitiesConfig) validateDataPlaneOperatorsIdentities(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	for operatorName, dataPlaneOperatorIdentity := range c.DataPlaneOperatorsIdentities {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Key(operatorName), &operatorName, nil)...)
		errs = append(errs, dataPlaneOperatorIdentity.validate(ctx, op, fldPath.Key(operatorName))...)
	}

	return errs
}

func (controlPlaneOperatorIdentity ControlPlaneOperatorIdentity) validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, controlPlaneOperatorIdentity.BaseOperatorIdentity.validate(ctx, op, fldPath)...)

	return errs
}

func (dataPlaneOperatorIdentity DataPlaneOperatorIdentity) validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, dataPlaneOperatorIdentity.BaseOperatorIdentity.validate(ctx, op, fldPath)...)

	errs = append(errs, validate.RequiredSlice(ctx, op, fldPath.Child("k8sServiceAccounts"), dataPlaneOperatorIdentity.KubernetesServiceAccounts, nil)...)
	// We validate that there are no duplicate Kubernetes Service Accounts.
	uniqueKubernetesServiceAccounts := make(map[string]bool)
	for i, kubernetesServiceAccount := range dataPlaneOperatorIdentity.KubernetesServiceAccounts {
		accountIDKey := kubernetesServiceAccount.Name + "$" + kubernetesServiceAccount.Namespace
		if uniqueKubernetesServiceAccounts[accountIDKey] {
			errs = append(errs, field.Duplicate(fldPath.Child("k8sServiceAccounts").Index(i), kubernetesServiceAccount))
		}
		uniqueKubernetesServiceAccounts[accountIDKey] = true
		errs = append(errs, kubernetesServiceAccount.validate(ctx, op, fldPath.Child("k8sServiceAccounts").Index(i))...)
	}

	return errs
}

func (baseOperatorIdentity BaseOperatorIdentity) validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredSlice(ctx, op, fldPath.Child("roleDefinitions"), baseOperatorIdentity.RoleDefinitions, nil)...)
	for i, roleDefinition := range baseOperatorIdentity.RoleDefinitions {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("roleDefinitions").Index(i).Child("name"), &roleDefinition.Name, nil)...)

		errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("roleDefinitions").Index(i).Child("resourceID"), roleDefinition.ResourceID, nil)...)
		errs = append(errs, azurevalidation.ValidateRoleDefinitionResourceID(ctx, op, fldPath.Child("roleDefinitions").Index(i).Child("resourceID"), roleDefinition.ResourceID)...)
	}

	var minVersionSemver semver.Version
	var errParsingMinVersion error
	if baseOperatorIdentity.MinOpenShiftVersion != "" {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("minOpenShiftVersion"), &baseOperatorIdentity.MinOpenShiftVersion, nil)...)
		errs = append(errs, validation.MatchesRegex(ctx, op, fldPath.Child("minOpenShiftVersion"), &baseOperatorIdentity.MinOpenShiftVersion, nil, ocpMaxMinVersionRegex, "must be in major.minor format")...)
		minVersionSemver, errParsingMinVersion = semver.ParseTolerant(baseOperatorIdentity.MinOpenShiftVersion)
		if errParsingMinVersion != nil {
			errs = append(errs, field.Invalid(fldPath.Child("minOpenShiftVersion"), baseOperatorIdentity.MinOpenShiftVersion, errParsingMinVersion.Error()))
		}
	}

	var maxVersionSemver semver.Version
	var errParsingMaxVersion error
	if baseOperatorIdentity.MaxOpenShiftVersion != "" {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maxOpenShiftVersion"), &baseOperatorIdentity.MaxOpenShiftVersion, nil)...)
		errs = append(errs, validation.MatchesRegex(ctx, op, fldPath.Child("maxOpenShiftVersion"), &baseOperatorIdentity.MaxOpenShiftVersion, nil, ocpMaxMinVersionRegex, "must be in major.minor format")...)
		maxVersionSemver, errParsingMaxVersion = semver.ParseTolerant(baseOperatorIdentity.MaxOpenShiftVersion)
		if errParsingMaxVersion != nil {
			errs = append(errs, field.Invalid(fldPath.Child("maxOpenShiftVersion"), baseOperatorIdentity.MaxOpenShiftVersion, errParsingMaxVersion.Error()))
		}
	}

	if errParsingMinVersion == nil && errParsingMaxVersion == nil && baseOperatorIdentity.MinOpenShiftVersion != "" && baseOperatorIdentity.MaxOpenShiftVersion != "" {
		if minVersionSemver.GT(maxVersionSemver) {
			errs = append(errs, field.Invalid(fldPath.Child("minOpenShiftVersion"), baseOperatorIdentity.MinOpenShiftVersion, "must be less than or equal to maxOpenShiftVersion"))
			errs = append(errs, field.Invalid(fldPath.Child("maxOpenShiftVersion"), baseOperatorIdentity.MaxOpenShiftVersion, "must be greater than or equal to minOpenShiftVersion"))
		}
	}

	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("requirement"), &baseOperatorIdentity.Requirement, nil, sets.New(AlwaysRequiredIdentityRequirement, RequiredOnEnablementIdentityRequirement))...)

	return errs
}

func (kubernetesServiceAccount KubernetesServiceAccount) validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("name"), &kubernetesServiceAccount.Name, nil)...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("namespace"), &kubernetesServiceAccount.Namespace, nil)...)

	return errs
}
