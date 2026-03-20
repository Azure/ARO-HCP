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

package validation

import (
	"context"
	"slices"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ValidateManagementClusterCreate validates a ManagementCluster for creation.
func ValidateManagementClusterCreate(ctx context.Context, newObj *api.ManagementCluster) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateManagementCluster(ctx, op, newObj, nil)
}

// ValidateManagementClusterUpdate validates a ManagementCluster for update.
func ValidateManagementClusterUpdate(ctx context.Context, newObj, oldObj *api.ManagementCluster) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return validateManagementCluster(ctx, op, newObj, oldObj)
}

var (
	toManagementClusterResourceID = func(oldObj *api.ManagementCluster) *azcorearm.ResourceID { return oldObj.ResourceID }
	toManagementClusterSpec       = func(oldObj *api.ManagementCluster) *api.ManagementClusterSpec { return &oldObj.Spec }
	toManagementClusterStatus     = func(oldObj *api.ManagementCluster) *api.ManagementClusterStatus { return &oldObj.Status }
)

func validateManagementCluster(ctx context.Context, op operation.Operation, newObj, oldObj *api.ManagementCluster) field.ErrorList {
	errs := field.ErrorList{}

	// ResourceID (top-level, mirrors CosmosMetadata.ResourceID)
	errs = append(errs, validate.RequiredPointer(ctx, op, field.NewPath("resourceId"), newObj.ResourceID, safe.Field(oldObj, toManagementClusterResourceID))...)
	errs = append(errs, validate.ImmutableByReflect(ctx, op, field.NewPath("resourceId"), newObj.ResourceID, safe.Field(oldObj, toManagementClusterResourceID))...)
	if newObj.ResourceID != nil {
		errs = append(errs, ValidateUUID(ctx, op, field.NewPath("resourceId").Child("name"), &newObj.ResourceID.Name, nil)...)
	}

	// Spec
	errs = append(errs, validateManagementClusterSpec(ctx, op, field.NewPath("spec"), &newObj.Spec, safe.Field(oldObj, toManagementClusterSpec))...)

	// Status
	errs = append(errs, validateManagementClusterStatus(ctx, op, field.NewPath("status"), &newObj.Status, safe.Field(oldObj, toManagementClusterStatus))...)

	// Cross-field: Ready=True requires all status fields to be populated
	errs = append(errs, validateReadyConditionRequiresCompleteStatus(newObj)...)

	return errs
}

var (
	toManagementClusterSpecSchedulingPolicy = func(oldObj *api.ManagementClusterSpec) *api.ManagementClusterSchedulingPolicy {
		return &oldObj.SchedulingPolicy
	}
)

func validateManagementClusterSpec(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ManagementClusterSpec) field.ErrorList {
	errs := field.ErrorList{}

	// SchedulingPolicy — required, must be a valid value
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("schedulingPolicy"), &newObj.SchedulingPolicy, safe.Field(oldObj, toManagementClusterSpecSchedulingPolicy))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("schedulingPolicy"), &newObj.SchedulingPolicy, safe.Field(oldObj, toManagementClusterSpecSchedulingPolicy), api.ValidManagementClusterSchedulingPolicies)...)

	return errs
}

var (
	toManagementClusterStatusAKSResourceID                            = func(oldObj *api.ManagementClusterStatus) *azcorearm.ResourceID { return oldObj.AKSResourceID }
	toManagementClusterStatusPublicDNSZoneResourceID                  = func(oldObj *api.ManagementClusterStatus) *azcorearm.ResourceID { return oldObj.PublicDNSZoneResourceID }
	toManagementClusterStatusCXSecretsKeyVaultURL                     = func(oldObj *api.ManagementClusterStatus) *string { return &oldObj.CXSecretsKeyVaultURL }
	toManagementClusterStatusCXManagedIdentitiesKeyVaultURL           = func(oldObj *api.ManagementClusterStatus) *string { return &oldObj.CXManagedIdentitiesKeyVaultURL }
	toManagementClusterStatusCXSecretsKeyVaultManagedIdentityClientID = func(oldObj *api.ManagementClusterStatus) *string {
		return &oldObj.CXSecretsKeyVaultManagedIdentityClientID
	}
	toManagementClusterStatusCSProvisionShardID = func(oldObj *api.ManagementClusterStatus) *string { return &oldObj.CSProvisionShardID }
	toManagementClusterStatusMaestroConfig      = func(oldObj *api.ManagementClusterStatus) *api.MaestroConfig { return &oldObj.MaestroConfig }
)

func validateManagementClusterStatus(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ManagementClusterStatus) field.ErrorList {
	errs := field.ErrorList{}

	// AKSResourceID — optional, but validated and immutable once set
	errs = append(errs, GenericResourceID(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterStatusAKSResourceID))...)
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterStatusAKSResourceID))...)

	// PublicDNSZoneResourceID — optional, but validated and immutable once set
	errs = append(errs, GenericResourceID(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterStatusPublicDNSZoneResourceID))...)
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterStatusPublicDNSZoneResourceID))...)

	// CXSecretsKeyVaultURL — optional, but validated and immutable once set
	errs = append(errs, URL(ctx, op, fldPath.Child("cxSecretsKeyVaultURL"), &newObj.CXSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusCXSecretsKeyVaultURL))...)
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("cxSecretsKeyVaultURL"), &newObj.CXSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusCXSecretsKeyVaultURL))...)

	// CXManagedIdentitiesKeyVaultURL — optional, but validated and immutable once set
	errs = append(errs, URL(ctx, op, fldPath.Child("cxManagedIdentitiesKeyVaultURL"), &newObj.CXManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusCXManagedIdentitiesKeyVaultURL))...)
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("cxManagedIdentitiesKeyVaultURL"), &newObj.CXManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusCXManagedIdentitiesKeyVaultURL))...)

	// CXSecretsKeyVaultManagedIdentityClientID — optional, but validated and immutable once set
	errs = append(errs, ValidateUUID(ctx, op, fldPath.Child("cxSecretsKeyVaultManagedIdentityClientID"), &newObj.CXSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterStatusCXSecretsKeyVaultManagedIdentityClientID))...)
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("cxSecretsKeyVaultManagedIdentityClientID"), &newObj.CXSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterStatusCXSecretsKeyVaultManagedIdentityClientID))...)

	// CSProvisionShardID — optional, but immutable once set
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("csProvisionShardID"), &newObj.CSProvisionShardID, safe.Field(oldObj, toManagementClusterStatusCSProvisionShardID))...)

	// MaestroConfig
	errs = append(errs, validateMaestroConfig(ctx, op, fldPath.Child("maestroConfig"), &newObj.MaestroConfig, safe.Field(oldObj, toManagementClusterStatusMaestroConfig))...)

	return errs
}

var (
	toMaestroConfigConsumerName  = func(oldObj *api.MaestroConfig) *string { return &oldObj.ConsumerName }
	toMaestroConfigRESTAPIConfig = func(oldObj *api.MaestroConfig) *api.MaestroRESTAPIConfig { return &oldObj.RESTAPIConfig }
	toMaestroConfigGRPCAPIConfig = func(oldObj *api.MaestroConfig) *api.MaestroGRPCAPIConfig { return &oldObj.GRPCAPIConfig }
)

var (
	toMaestroRESTAPIConfigURL = func(oldObj *api.MaestroRESTAPIConfig) *string { return &oldObj.URL }
	toMaestroGRPCAPIConfigURL = func(oldObj *api.MaestroGRPCAPIConfig) *string { return &oldObj.URL }
)

func validateMaestroConfig(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.MaestroConfig) field.ErrorList {
	errs := field.ErrorList{}

	// ConsumerName — optional, but immutable once set
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("consumerName"), &newObj.ConsumerName, safe.Field(oldObj, toMaestroConfigConsumerName))...)

	// RESTAPIConfig.URL — optional, but validated and immutable once set
	restOldObj := safe.Field(oldObj, toMaestroConfigRESTAPIConfig)
	errs = append(errs, URL(ctx, op, fldPath.Child("restAPIConfig").Child("url"), &newObj.RESTAPIConfig.URL, safe.Field(restOldObj, toMaestroRESTAPIConfigURL))...)
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("restAPIConfig").Child("url"), &newObj.RESTAPIConfig.URL, safe.Field(restOldObj, toMaestroRESTAPIConfigURL))...)

	// GRPCAPIConfig.URL — optional, but validated and immutable once set
	grpcOldObj := safe.Field(oldObj, toMaestroConfigGRPCAPIConfig)
	errs = append(errs, URL(ctx, op, fldPath.Child("grpcAPIConfig").Child("url"), &newObj.GRPCAPIConfig.URL, safe.Field(grpcOldObj, toMaestroGRPCAPIConfigURL))...)
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("grpcAPIConfig").Child("url"), &newObj.GRPCAPIConfig.URL, safe.Field(grpcOldObj, toMaestroGRPCAPIConfigURL))...)

	return errs
}

// validateReadyConditionRequiresCompleteStatus checks that if the Ready condition
// is True, all status fields required for a functioning management cluster are populated.
func validateReadyConditionRequiresCompleteStatus(mc *api.ManagementCluster) field.ErrorList {
	readyConditionTrue := slices.ContainsFunc(mc.Status.Conditions, func(c api.Condition) bool {
		return c.Type == string(api.ManagementClusterConditionReady) && c.Status == api.ConditionTrue
	})

	if !readyConditionTrue {
		return nil
	}

	errs := field.ErrorList{}
	condPath := field.NewPath("status", "conditions").Key("Ready")

	if mc.Status.AKSResourceID == nil {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.aksResourceID to be set"))
	}
	if mc.Status.PublicDNSZoneResourceID == nil {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.publicDNSZoneResourceID to be set"))
	}
	if len(mc.Status.CXSecretsKeyVaultURL) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.cxSecretsKeyVaultURL to be set"))
	}
	if len(mc.Status.CXManagedIdentitiesKeyVaultURL) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.cxManagedIdentitiesKeyVaultURL to be set"))
	}
	if len(mc.Status.CXSecretsKeyVaultManagedIdentityClientID) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.cxSecretsKeyVaultManagedIdentityClientID to be set"))
	}
	if len(mc.Status.CSProvisionShardID) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.csProvisionShardID to be set"))
	}
	if len(mc.Status.MaestroConfig.ConsumerName) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.maestroConfig.consumerName to be set"))
	}
	if len(mc.Status.MaestroConfig.RESTAPIConfig.URL) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.maestroConfig.restAPIConfig.url to be set"))
	}
	if len(mc.Status.MaestroConfig.GRPCAPIConfig.URL) == 0 {
		errs = append(errs, field.Invalid(condPath, "True", "Ready condition requires status.maestroConfig.grpcAPIConfig.url to be set"))
	}

	return errs
}
