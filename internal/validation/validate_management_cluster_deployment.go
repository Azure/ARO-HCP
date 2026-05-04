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

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

// ValidateManagementClusterDeploymentCreate validates a ManagementClusterDeployment for creation.
func ValidateManagementClusterDeploymentCreate(ctx context.Context, newObj *fleet.ManagementClusterDeployment) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateManagementClusterDeployment(ctx, op, newObj, nil)
}

// ValidateManagementClusterDeploymentUpdate validates a ManagementClusterDeployment for update.
func ValidateManagementClusterDeploymentUpdate(ctx context.Context, newObj, oldObj *fleet.ManagementClusterDeployment) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return validateManagementClusterDeployment(ctx, op, newObj, oldObj)
}

var (
	toManagementClusterDeploymentSpec = func(oldObj *fleet.ManagementClusterDeployment) *fleet.ManagementClusterDeploymentSpec {
		return &oldObj.Spec
	}
	toManagementClusterDeploymentStatus = func(oldObj *fleet.ManagementClusterDeployment) *fleet.ManagementClusterDeploymentStatus {
		return &oldObj.Status
	}
)

func validateManagementClusterDeployment(ctx context.Context, op operation.Operation, newObj, oldObj *fleet.ManagementClusterDeployment) field.ErrorList {
	errs := field.ErrorList{}

	// Spec
	errs = append(errs, validateManagementClusterDeploymentSpec(ctx, op, field.NewPath("spec"), &newObj.Spec, safe.Field(oldObj, toManagementClusterDeploymentSpec))...)

	// Status
	errs = append(errs, validateManagementClusterDeploymentStatus(ctx, op, field.NewPath("status"), &newObj.Status, safe.Field(oldObj, toManagementClusterDeploymentStatus))...)

	return errs
}

var (
	toManagementClusterDeploymentSpecStampIdentifier = func(oldObj *fleet.ManagementClusterDeploymentSpec) *string {
		return &oldObj.StampIdentifier
	}
)

func validateManagementClusterDeploymentSpec(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *fleet.ManagementClusterDeploymentSpec) field.ErrorList {
	errs := field.ErrorList{}

	// StampIdentifier — required, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("stampIdentifier"), &newObj.StampIdentifier, safe.Field(oldObj, toManagementClusterDeploymentSpecStampIdentifier))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("stampIdentifier"), &newObj.StampIdentifier, safe.Field(oldObj, toManagementClusterDeploymentSpecStampIdentifier))...)

	return errs
}

var (
	toManagementClusterDeploymentStatusAKSResourceID = func(oldObj *fleet.ManagementClusterDeploymentStatus) *azcorearm.ResourceID {
		return oldObj.AKSResourceID
	}
	toManagementClusterDeploymentStatusPublicDNSZoneResourceID = func(oldObj *fleet.ManagementClusterDeploymentStatus) *azcorearm.ResourceID {
		return oldObj.PublicDNSZoneResourceID
	}
	toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultURL = func(oldObj *fleet.ManagementClusterDeploymentStatus) *string {
		return &oldObj.HostedClustersSecretsKeyVaultURL
	}
	toManagementClusterDeploymentStatusHostedClustersManagedIdentitiesKeyVaultURL = func(oldObj *fleet.ManagementClusterDeploymentStatus) *string {
		return &oldObj.HostedClustersManagedIdentitiesKeyVaultURL
	}
	toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultManagedIdentityClientID = func(oldObj *fleet.ManagementClusterDeploymentStatus) *string {
		return &oldObj.HostedClustersSecretsKeyVaultManagedIdentityClientID
	}
	toManagementClusterDeploymentStatusMaestroConsumerName = func(oldObj *fleet.ManagementClusterDeploymentStatus) *string {
		return &oldObj.MaestroConsumerName
	}
	toManagementClusterDeploymentStatusMaestroRESTAPIURL = func(oldObj *fleet.ManagementClusterDeploymentStatus) *string {
		return &oldObj.MaestroRESTAPIURL
	}
	toManagementClusterDeploymentStatusMaestroGRPCTarget = func(oldObj *fleet.ManagementClusterDeploymentStatus) *string {
		return &oldObj.MaestroGRPCTarget
	}
	toManagementClusterDeploymentStatusManagementClusterID = func(oldObj *fleet.ManagementClusterDeploymentStatus) *azcorearm.ResourceID {
		return oldObj.ManagementClusterID
	}
)

func validateManagementClusterDeploymentStatus(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *fleet.ManagementClusterDeploymentStatus) field.ErrorList {
	errs := field.ErrorList{}

	// AKSResourceID — required, validated as AKS resource type, immutable
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterDeploymentStatusAKSResourceID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterDeploymentStatusAKSResourceID), "Microsoft.ContainerService/managedClusters")...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterDeploymentStatusAKSResourceID))...)

	// PublicDNSZoneResourceID — required, validated as DNS zone resource type, immutable
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterDeploymentStatusPublicDNSZoneResourceID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterDeploymentStatusPublicDNSZoneResourceID), "Microsoft.Network/dnszones")...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterDeploymentStatusPublicDNSZoneResourceID))...)

	// HostedClustersSecretsKeyVaultURL — required, validated as URL, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultURL"), &newObj.HostedClustersSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultURL))...)
	errs = append(errs, URL(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultURL"), &newObj.HostedClustersSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultURL))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultURL"), &newObj.HostedClustersSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultURL))...)

	// HostedClustersManagedIdentitiesKeyVaultURL — required, validated as URL, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostedClustersManagedIdentitiesKeyVaultURL"), &newObj.HostedClustersManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersManagedIdentitiesKeyVaultURL))...)
	errs = append(errs, URL(ctx, op, fldPath.Child("hostedClustersManagedIdentitiesKeyVaultURL"), &newObj.HostedClustersManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersManagedIdentitiesKeyVaultURL))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostedClustersManagedIdentitiesKeyVaultURL"), &newObj.HostedClustersManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersManagedIdentitiesKeyVaultURL))...)

	// HostedClustersSecretsKeyVaultManagedIdentityClientID — required, validated as UUID, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultManagedIdentityClientID"), &newObj.HostedClustersSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultManagedIdentityClientID))...)
	errs = append(errs, ValidateUUID(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultManagedIdentityClientID"), &newObj.HostedClustersSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultManagedIdentityClientID))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultManagedIdentityClientID"), &newObj.HostedClustersSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterDeploymentStatusHostedClustersSecretsKeyVaultManagedIdentityClientID))...)

	// MaestroConsumerName — required, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maestroConsumerName"), &newObj.MaestroConsumerName, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroConsumerName))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("maestroConsumerName"), &newObj.MaestroConsumerName, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroConsumerName))...)

	// MaestroRESTAPIURL — required, validated as URL, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maestroRESTAPIURL"), &newObj.MaestroRESTAPIURL, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroRESTAPIURL))...)
	errs = append(errs, URL(ctx, op, fldPath.Child("maestroRESTAPIURL"), &newObj.MaestroRESTAPIURL, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroRESTAPIURL))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("maestroRESTAPIURL"), &newObj.MaestroRESTAPIURL, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroRESTAPIURL))...)

	// MaestroGRPCTarget — required, validated as HostPort, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maestroGRPCTarget"), &newObj.MaestroGRPCTarget, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroGRPCTarget))...)
	errs = append(errs, HostPort(ctx, op, fldPath.Child("maestroGRPCTarget"), &newObj.MaestroGRPCTarget, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroGRPCTarget))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("maestroGRPCTarget"), &newObj.MaestroGRPCTarget, safe.Field(oldObj, toManagementClusterDeploymentStatusMaestroGRPCTarget))...)

	// ManagementClusterID — optional, immutable once set
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("managementClusterID"), newObj.ManagementClusterID, safe.Field(oldObj, toManagementClusterDeploymentStatusManagementClusterID))...)

	return errs
}
