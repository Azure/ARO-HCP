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
	errs = append(errs, immutableByReflect(ctx, op, field.NewPath("resourceId"), newObj.ResourceID, safe.Field(oldObj, toManagementClusterResourceID))...)

	// Spec
	errs = append(errs, validateManagementClusterSpec(ctx, op, field.NewPath("spec"), &newObj.Spec, safe.Field(oldObj, toManagementClusterSpec))...)

	// Status
	errs = append(errs, validateManagementClusterStatus(ctx, op, field.NewPath("status"), &newObj.Status, safe.Field(oldObj, toManagementClusterStatus))...)

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
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("schedulingPolicy"), &newObj.SchedulingPolicy, safe.Field(oldObj, toManagementClusterSpecSchedulingPolicy), api.ValidManagementClusterSchedulingPolicies, nil)...)

	return errs
}

var (
	toManagementClusterStatusAKSResourceID                              = func(oldObj *api.ManagementClusterStatus) *azcorearm.ResourceID { return oldObj.AKSResourceID }
	toManagementClusterStatusPublicDNSZoneResourceID                    = func(oldObj *api.ManagementClusterStatus) *azcorearm.ResourceID { return oldObj.PublicDNSZoneResourceID }
	toManagementClusterStatusHostedClustersSecretsKeyVaultURL           = func(oldObj *api.ManagementClusterStatus) *string { return &oldObj.HostedClustersSecretsKeyVaultURL }
	toManagementClusterStatusHostedClustersManagedIdentitiesKeyVaultURL = func(oldObj *api.ManagementClusterStatus) *string {
		return &oldObj.HostedClustersManagedIdentitiesKeyVaultURL
	}
	toManagementClusterStatusHostedClustersSecretsKeyVaultManagedIdentityClientID = func(oldObj *api.ManagementClusterStatus) *string {
		return &oldObj.HostedClustersSecretsKeyVaultManagedIdentityClientID
	}
	toManagementClusterStatusClusterServiceProvisionShardID = func(oldObj *api.ManagementClusterStatus) *api.InternalID {
		return oldObj.ClusterServiceProvisionShardID
	}
	toManagementClusterStatusMaestroConsumerName = func(oldObj *api.ManagementClusterStatus) *string {
		return &oldObj.MaestroConsumerName
	}
	toManagementClusterStatusMaestroRESTAPIURL = func(oldObj *api.ManagementClusterStatus) *string {
		return &oldObj.MaestroRESTAPIURL
	}
	toManagementClusterStatusMaestroGRPCTarget = func(oldObj *api.ManagementClusterStatus) *string {
		return &oldObj.MaestroGRPCTarget
	}
)

func validateManagementClusterStatus(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ManagementClusterStatus) field.ErrorList {
	errs := field.ErrorList{}

	// AKSResourceID — required, validated as AKS resource type, immutable
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterStatusAKSResourceID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterStatusAKSResourceID), "Microsoft.ContainerService/managedClusters")...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("aksResourceID"), newObj.AKSResourceID, safe.Field(oldObj, toManagementClusterStatusAKSResourceID))...)

	// PublicDNSZoneResourceID — required, validated as DNS zone resource type, immutable
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterStatusPublicDNSZoneResourceID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterStatusPublicDNSZoneResourceID), "Microsoft.Network/dnszones")...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("publicDNSZoneResourceID"), newObj.PublicDNSZoneResourceID, safe.Field(oldObj, toManagementClusterStatusPublicDNSZoneResourceID))...)

	// HostedClustersSecretsKeyVaultURL — required, validated as URL, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultURL"), &newObj.HostedClustersSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusHostedClustersSecretsKeyVaultURL))...)
	errs = append(errs, URL(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultURL"), &newObj.HostedClustersSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusHostedClustersSecretsKeyVaultURL))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultURL"), &newObj.HostedClustersSecretsKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusHostedClustersSecretsKeyVaultURL))...)

	// HostedClustersManagedIdentitiesKeyVaultURL — required, validated as URL, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostedClustersManagedIdentitiesKeyVaultURL"), &newObj.HostedClustersManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusHostedClustersManagedIdentitiesKeyVaultURL))...)
	errs = append(errs, URL(ctx, op, fldPath.Child("hostedClustersManagedIdentitiesKeyVaultURL"), &newObj.HostedClustersManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusHostedClustersManagedIdentitiesKeyVaultURL))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostedClustersManagedIdentitiesKeyVaultURL"), &newObj.HostedClustersManagedIdentitiesKeyVaultURL, safe.Field(oldObj, toManagementClusterStatusHostedClustersManagedIdentitiesKeyVaultURL))...)

	// HostedClustersSecretsKeyVaultManagedIdentityClientID — required, validated as UUID, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultManagedIdentityClientID"), &newObj.HostedClustersSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterStatusHostedClustersSecretsKeyVaultManagedIdentityClientID))...)
	errs = append(errs, ValidateUUID(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultManagedIdentityClientID"), &newObj.HostedClustersSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterStatusHostedClustersSecretsKeyVaultManagedIdentityClientID))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostedClustersSecretsKeyVaultManagedIdentityClientID"), &newObj.HostedClustersSecretsKeyVaultManagedIdentityClientID, safe.Field(oldObj, toManagementClusterStatusHostedClustersSecretsKeyVaultManagedIdentityClientID))...)

	// ClusterServiceProvisionShardID — required, immutable
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("clusterServiceProvisionShardID"), newObj.ClusterServiceProvisionShardID, safe.Field(oldObj, toManagementClusterStatusClusterServiceProvisionShardID))...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("clusterServiceProvisionShardID"), newObj.ClusterServiceProvisionShardID, safe.Field(oldObj, toManagementClusterStatusClusterServiceProvisionShardID))...)

	// MaestroConsumerName — required, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maestroConsumerName"), &newObj.MaestroConsumerName, safe.Field(oldObj, toManagementClusterStatusMaestroConsumerName))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("maestroConsumerName"), &newObj.MaestroConsumerName, safe.Field(oldObj, toManagementClusterStatusMaestroConsumerName))...)

	// MaestroRESTAPIURL — required, validated as URL, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maestroRESTAPIURL"), &newObj.MaestroRESTAPIURL, safe.Field(oldObj, toManagementClusterStatusMaestroRESTAPIURL))...)
	errs = append(errs, URL(ctx, op, fldPath.Child("maestroRESTAPIURL"), &newObj.MaestroRESTAPIURL, safe.Field(oldObj, toManagementClusterStatusMaestroRESTAPIURL))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("maestroRESTAPIURL"), &newObj.MaestroRESTAPIURL, safe.Field(oldObj, toManagementClusterStatusMaestroRESTAPIURL))...)

	// MaestroGRPCTarget — required, immutable
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maestroGRPCTarget"), &newObj.MaestroGRPCTarget, safe.Field(oldObj, toManagementClusterStatusMaestroGRPCTarget))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("maestroGRPCTarget"), &newObj.MaestroGRPCTarget, safe.Field(oldObj, toManagementClusterStatusMaestroGRPCTarget))...)

	return errs
}
