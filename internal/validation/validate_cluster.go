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
	"fmt"
	"net"
	"strings"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func ValidateClusterCreate(ctx context.Context, newCluster *api.HCPOpenShiftCluster, validationPathMapper api.ValidationPathMapperFunc) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateCluster(ctx, op, newCluster, nil, validationPathMapper)
}

func ValidateClusterUpdate(ctx context.Context, newCluster, oldCluster *api.HCPOpenShiftCluster, validationPathMapper api.ValidationPathMapperFunc) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return validateCluster(ctx, op, newCluster, oldCluster, validationPathMapper)
}

var (
	toTrackedResource           = func(oldObj *api.HCPOpenShiftCluster) *arm.TrackedResource { return &oldObj.TrackedResource }
	toClusterCustomerProperties = func(oldObj *api.HCPOpenShiftCluster) *api.HCPOpenShiftClusterCustomerProperties {
		return &oldObj.CustomerProperties
	}
	toClusterServiceProviderProperties = func(oldObj *api.HCPOpenShiftCluster) *api.HCPOpenShiftClusterServiceProviderProperties {
		return &oldObj.ServiceProviderProperties
	}
	toClusterIdentity = func(oldObj *api.HCPOpenShiftCluster) *arm.ManagedServiceIdentity { return oldObj.Identity }
)

func validateCluster(ctx context.Context, op operation.Operation, newCluster, oldCluster *api.HCPOpenShiftCluster, validationPathMapper api.ValidationPathMapperFunc) field.ErrorList {
	errs := field.ErrorList{}

	//arm.TrackedResource
	errs = append(errs, validateTrackedResource(ctx, op, field.NewPath("trackedResource"), &newCluster.TrackedResource, safe.Field(oldCluster, toTrackedResource))...)

	// Properties HCPOpenShiftClusterCustomerProperties `json:"properties,omitempty" validate:"required"`
	errs = append(errs, validateClusterCustomerProperties(ctx, op, field.NewPath("customerProperties"), &newCluster.CustomerProperties, safe.Field(oldCluster, toClusterCustomerProperties))...)

	// Properties HCPOpenShiftClusterCustomerProperties `json:"properties,omitempty" validate:"required"`
	errs = append(errs, validateClusterServiceProviderProperties(ctx, op, field.NewPath("serviceProviderProperties"), &newCluster.ServiceProviderProperties, safe.Field(oldCluster, toClusterServiceProviderProperties))...)

	// Identity   *arm.ManagedServiceIdentity   `json:"identity,omitempty"   validate:"omitempty"`
	errs = append(errs, validateManagedServiceIdentity(ctx, op, field.NewPath("identity"), newCluster.Identity, safe.Field(oldCluster, toClusterIdentity))...)

	// there several resourceIDs that must be verified with respect to this ID.  This is the only level of validation with access to both
	errs = append(errs, validateResourceIDsAgainstClusterID(ctx, op, newCluster, oldCluster)...)

	// there are pieces of clusterProperties that are dependent upon values in .identity
	errs = append(errs, validateOperatorAuthenticationAgainstIdentities(ctx, op, newCluster, oldCluster)...)

	RewriteValidationFieldPaths(errs, validationPathMapper)

	return errs
}

func validateOperatorAuthenticationAgainstIdentities(ctx context.Context, op operation.Operation, newCluster, _ *api.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	// Verify that every key in Identity.UserAssignedIdentities is referenced
	// exactly once by either ControlPlaneOperators or ServiceManagedIdentity.

	userAssignedIdentities := make(map[string]int)
	if newCluster.Identity != nil {
		for key := range newCluster.Identity.UserAssignedIdentities {
			// Resource IDs are case-insensitive. Don't assume they
			// have consistent casing, even within the same resource.
			userAssignedIdentities[strings.ToLower(key)] = 0
		}
	}

	tallyIdentity := func(identity string, fldPath *field.Path) {
		key := strings.ToLower(identity)
		if _, ok := userAssignedIdentities[key]; ok {
			userAssignedIdentities[key]++
		} else {
			errs = append(errs, field.Invalid(fldPath, identity, "identity is not assigned to this resource"))
		}
	}

	for operatorName, operatorIdentity := range newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "controlPlaneOperators").Key(operatorName)
		tallyIdentity(operatorIdentity, fldPath)
	}

	if serviceManagedIdentity := newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity; len(serviceManagedIdentity) != 0 {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "serviceManagedIdentity")
		tallyIdentity(serviceManagedIdentity, fldPath)
	}

	if newCluster.Identity != nil {
		for identity := range newCluster.Identity.UserAssignedIdentities {
			fldPath := field.NewPath("identity", "userAssignedIdentities").Key(identity)
			key := strings.ToLower(identity)
			if tally, ok := userAssignedIdentities[key]; ok {
				switch tally {
				case 0:
					errs = append(errs, field.Invalid(fldPath, identity, "identity is assigned to this resource but not used"))
				case 1:
					// Valid: Identity is referenced once.
				default:
					errs = append(errs, field.Invalid(fldPath, identity, "identity is used multiple times"))
				}
			}
		}
	}

	// Data-plane operator identities must not be assigned to this resource.
	for operatorName, operatorIdentity := range newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "dataPlaneOperators").Key(operatorName)
		key := strings.ToLower(operatorIdentity)
		if _, ok := userAssignedIdentities[key]; ok {
			errs = append(errs, field.Invalid(fldPath, operatorIdentity, "cannot use identity assigned to this resource by .identities.userAssignedIdentities"))
		}
	}

	return errs
}

func validateResourceIDsAgainstClusterID(ctx context.Context, op operation.Operation, newCluster, _ *api.HCPOpenShiftCluster) field.ErrorList {
	if newCluster.ID == nil {
		return nil
	}

	errs := field.ErrorList{}

	// Validate that managed resource group is different from cluster resource group
	errs = append(errs, DifferentResourceGroupName(ctx, op, field.NewPath("customerProperties", "platform", "managedResourceGroup"), &newCluster.CustomerProperties.Platform.ManagedResourceGroup, nil, newCluster.ID.ResourceGroupName)...)
	errs = append(errs, SameSubscription(ctx, op, field.NewPath("customerProperties", "platform", "subnetId"), &newCluster.CustomerProperties.Platform.SubnetID, nil, newCluster.ID.SubscriptionID)...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, field.NewPath("customerProperties", "platform", "subnetId"), &newCluster.CustomerProperties.Platform.SubnetID, nil, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)

	for operatorName, operatorIdentity := range newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "controlPlaneOperators").Key(operatorName)
		errs = append(errs, ValidateUserAssignedIdentityLocation(ctx, op, fldPath, &operatorIdentity, nil, newCluster.ID.SubscriptionID, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)
	}
	for operatorName, operatorIdentity := range newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "dataPlaneOperators").Key(operatorName)
		errs = append(errs, ValidateUserAssignedIdentityLocation(ctx, op, fldPath, &operatorIdentity, nil, newCluster.ID.SubscriptionID, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)
	}
	errs = append(errs, ValidateUserAssignedIdentityLocation(ctx, op,
		field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "serviceManagedIdentity"),
		&newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity, nil,
		newCluster.ID.SubscriptionID, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)

	return errs
}

var (
	toTrackedResourceResource = func(oldObj *arm.TrackedResource) *arm.Resource { return &oldObj.Resource }
	toTrackedResourceLocation = func(oldObj *arm.TrackedResource) *string { return &oldObj.Location }
)

func validateTrackedResource(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *arm.TrackedResource) field.ErrorList {
	errs := field.ErrorList{}

	//Resource
	errs = append(errs, validateResource(ctx, op, fldPath.Child("resource"), &newObj.Resource, safe.Field(oldObj, toTrackedResourceResource))...)

	//Location string            `json:"location,omitempty" visibility:"read create"        validate:"required"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("location"), &newObj.Location, safe.Field(oldObj, toTrackedResourceLocation))...)
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("location"), &newObj.Location, safe.Field(oldObj, toTrackedResourceLocation))...)

	//Tags     map[string]string `json:"tags,omitempty"     visibility:"read create update"`

	return errs
}

var (
	toResourceID         = func(oldObj *arm.Resource) *azcorearm.ResourceID { return oldObj.ID }
	toResourceName       = func(oldObj *arm.Resource) *string { return &oldObj.Name }
	toResourceType       = func(oldObj *arm.Resource) *string { return &oldObj.Type }
	toResourceSystemData = func(oldObj *arm.Resource) *arm.SystemData { return oldObj.SystemData }
)

// Version                 VersionProfile              `json:"version,omitempty"`
func validateResource(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *arm.Resource) field.ErrorList {
	errs := field.ErrorList{}

	//ID         string      `json:"id,omitempty"         visibility:"read"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("id"), newObj.ID, safe.Field(oldObj, toResourceID))...)
	// TODO need to determine whether can require this on pre-flight checks
	//errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("id"), newObj.ID, safe.Field(oldObj, toResourceID))...)

	//Name       string      `json:"name,omitempty"       visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toResourceName))...)

	//Type       string      `json:"type,omitempty"       visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toResourceType))...)

	//SystemData *SystemData `json:"systemData,omitempty" visibility:"read"`
	errs = append(errs, validateSystemData(ctx, op, fldPath.Child("systemData"), newObj.SystemData, safe.Field(oldObj, toResourceSystemData))...)

	return errs
}

// Version                 VersionProfile              `json:"version,omitempty"`
func validateSystemData(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *arm.SystemData) field.ErrorList {
	errs := field.ErrorList{}

	//CreatedBy string `json:"createdBy,omitempty"`
	//CreatedByType CreatedByType `json:"createdByType,omitempty"`
	//CreatedAt *time.Time `json:"createdAt,omitempty"`
	//LastModifiedBy string `json:"lastModifiedBy,omitempty"`
	//LastModifiedByType CreatedByType `json:"lastModifiedByType,omitempty"`
	//LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`

	return errs
}

var (
	toVersion          = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.VersionProfile { return &oldObj.Version }
	toCustomerDNS      = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.CustomerDNSProfile { return &oldObj.DNS }
	toNetwork          = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.NetworkProfile { return &oldObj.Network }
	toCustomerAPI      = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.CustomerAPIProfile { return &oldObj.API }
	toCustomerPlatform = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.CustomerPlatformProfile {
		return &oldObj.Platform
	}
	toClusterAutoscaling = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.ClusterAutoscalingProfile {
		return &oldObj.Autoscaling
	}
	toNodeDrainTimeoutMinutes = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *int32 { return &oldObj.NodeDrainTimeoutMinutes }
	toEtcd                    = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.EtcdProfile { return &oldObj.Etcd }
	toClusterImageRegistry    = func(oldObj *api.HCPOpenShiftClusterCustomerProperties) *api.ClusterImageRegistryProfile {
		return &oldObj.ClusterImageRegistry
	}
)

func validateClusterCustomerProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterCustomerProperties) field.ErrorList {
	errs := field.ErrorList{}

	// Version                 VersionProfile              `json:"version,omitempty"`
	errs = append(errs, validateVersionProfile(ctx, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, toVersion))...)

	// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
	errs = append(errs, validateCustomerDNSProfile(ctx, op, fldPath.Child("dns"), &newObj.DNS, safe.Field(oldObj, toCustomerDNS))...)

	// Network                 NetworkProfile              `json:"network,omitempty"                 visibility:"read create"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("network"), &newObj.Network, safe.Field(oldObj, toNetwork))...)
	errs = append(errs, validateNetworkProfile(ctx, op, fldPath.Child("network"), &newObj.Network, safe.Field(oldObj, toNetwork))...)

	// API                     CustomerAPIProfile                  `json:"api,omitempty"`
	errs = append(errs, validateCustomerAPIProfile(ctx, op, fldPath.Child("api"), &newObj.API, safe.Field(oldObj, toCustomerAPI))...)

	// Platform                CustomerPlatformProfile             `json:"platform,omitempty"                visibility:"read create"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toCustomerPlatform))...)
	errs = append(errs, validateCustomerPlatformProfile(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toCustomerPlatform))...)

	//Autoscaling             ClusterAutoscalingProfile   `json:"autoscaling,omitempty"             visibility:"read create update"`
	errs = append(errs, validateClusterAutoscalingProfile(ctx, op, fldPath.Child("autoscaling"), &newObj.Autoscaling, safe.Field(oldObj, toClusterAutoscaling))...)

	//NodeDrainTimeoutMinutes int32                       `json:"nodeDrainTimeoutMinutes,omitempty" visibility:"read create update" validate:"omitempty,min=0,max=10080"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("nodeDrainTimeoutMinutes"), &newObj.NodeDrainTimeoutMinutes, safe.Field(oldObj, toNodeDrainTimeoutMinutes), 0)...)
	errs = append(errs, Maximum(ctx, op, fldPath.Child("nodeDrainTimeoutMinutes"), &newObj.NodeDrainTimeoutMinutes, safe.Field(oldObj, toNodeDrainTimeoutMinutes), 10080)...)

	//Etcd                    EtcdProfile                 `json:"etcd,omitempty"                    visibility:"read create"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("etcd"), &newObj.Etcd, safe.Field(oldObj, toEtcd))...)
	errs = append(errs, validateEtcdProfile(ctx, op, fldPath.Child("etcd"), &newObj.Etcd, safe.Field(oldObj, toEtcd))...)

	//ClusterImageRegistry    ClusterImageRegistryProfile `json:"clusterImageRegistry,omitempty"    visibility:"read create"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("clusterImageRegistry"), &newObj.ClusterImageRegistry, safe.Field(oldObj, toClusterImageRegistry))...)
	errs = append(errs, validateClusterImageRegistryProfile(ctx, op, fldPath.Child("clusterImageRegistry"), &newObj.ClusterImageRegistry, safe.Field(oldObj, toClusterImageRegistry))...)

	return errs
}

var (
	toHCPOpenShiftClusterServiceProviderPropertiesProvisioningState = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *arm.ProvisioningState {
		return &oldObj.ProvisioningState
	}
	toServiceProviderDNS = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *api.ServiceProviderDNSProfile {
		return &oldObj.DNS
	}
	toServiceProviderCosmosUID = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *string {
		return &oldObj.CosmosUID
	}
	toServiceProviderClusterServiceID = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *api.InternalID {
		return &oldObj.ClusterServiceID
	}
	toServiceProviderConsole = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *api.ServiceProviderConsoleProfile {
		return &oldObj.Console
	}
	toServiceProviderAPI = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *api.ServiceProviderAPIProfile {
		return &oldObj.API
	}
	toServiceProviderPlatform = func(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *api.ServiceProviderPlatformProfile {
		return &oldObj.Platform
	}
)

func validateClusterServiceProviderProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	// ProvisioningState       arm.ProvisioningState       `json:"provisioningState,omitempty"       visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("provisioningState"), &newObj.ProvisioningState, safe.Field(oldObj, toHCPOpenShiftClusterServiceProviderPropertiesProvisioningState))...)

	//CosmosUID         string                         `json:"cosmosUID,omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("cosmosUID"), &newObj.CosmosUID, safe.Field(oldObj, toServiceProviderCosmosUID))...)
	if oldObj == nil { // must be unset on creation because we don't know it yet.
		errs = append(errs, validate.ForbiddenValue(ctx, op, fldPath.Child("cosmosUID"), &newObj.CosmosUID, nil)...)
	}

	//ClusterServiceID  InternalID                     `json:"clusterServiceID,omitempty"                visibility:"read"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("clusterServiceID"), &newObj.ClusterServiceID, safe.Field(oldObj, toServiceProviderClusterServiceID))...)

	// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
	errs = append(errs, validateServiceProviderDNSProfile(ctx, op, fldPath.Child("dns"), &newObj.DNS, safe.Field(oldObj, toServiceProviderDNS))...)

	// Console                 ServiceProviderConsoleProfile              `json:"console,omitempty"                 visibility:"read"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("console"), &newObj.Console, safe.Field(oldObj, toServiceProviderConsole))...)
	errs = append(errs, validateServiceProviderConsoleProfile(ctx, op, fldPath.Child("console"), &newObj.Console, safe.Field(oldObj, toServiceProviderConsole))...)

	// API                     CustomerAPIProfile                  `json:"api,omitempty"`
	errs = append(errs, validateServiceProviderAPIProfile(ctx, op, fldPath.Child("api"), &newObj.API, safe.Field(oldObj, toServiceProviderAPI))...)

	// Platform                CustomerPlatformProfile             `json:"platform,omitempty"                visibility:"read create"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toServiceProviderPlatform))...)
	errs = append(errs, validateServiceProviderPlatformProfile(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toServiceProviderPlatform))...)

	return errs
}

var (
	toVersionID    = func(oldObj *api.VersionProfile) *string { return &oldObj.ID }
	toChannelGroup = func(oldObj *api.VersionProfile) *string { return &oldObj.ChannelGroup }
)

// Version                 VersionProfile              `json:"version,omitempty"`
func validateVersionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.VersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	// Version should be immutable once is created
	// additional validations may depend on the subscription, hence they will be done in the admission package
	// ID           string `json:"id,omitempty"                visibility:"read create"        validate:"required_unless=ChannelGroup stable,omitempty,openshift_version"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toVersionID))...)

	// ChannelGroup string `json:"channelGroup,omitempty"      visibility:"read create update"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, safe.Field(oldObj, toChannelGroup))...)

	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, nil)...)

	// Version ID is required for non-stable channel groups
	if newObj.ChannelGroup != "stable" {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("id"), &newObj.ID, nil)...)
	}

	return errs
}

var (
	toDNSBaseDomainPrefix = func(oldObj *api.CustomerDNSProfile) *string { return &oldObj.BaseDomainPrefix }
)

// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
func validateCustomerDNSProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.CustomerDNSProfile) field.ErrorList {
	errs := field.ErrorList{}

	// BaseDomainPrefix string `json:"baseDomainPrefix,omitempty" visibility:"read create" validate:"omitempty,dns_rfc1035_label,max=15"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("baseDomainPrefix"), &newObj.BaseDomainPrefix, safe.Field(oldObj, toDNSBaseDomainPrefix))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("baseDomainPrefix"), &newObj.BaseDomainPrefix, nil, 15)...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("baseDomainPrefix"), &newObj.BaseDomainPrefix, nil, rfc1035LabelRegex, rfc1035ErrorString)...)

	return errs
}

var (
	toDNSBaseDomain = func(oldObj *api.ServiceProviderDNSProfile) *string { return &oldObj.BaseDomain }
)

// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
func validateServiceProviderDNSProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ServiceProviderDNSProfile) field.ErrorList {
	errs := field.ErrorList{}

	// BaseDomain       string `json:"baseDomain,omitempty"       visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("baseDomain"), &newObj.BaseDomain, safe.Field(oldObj, toDNSBaseDomain))...)

	return errs
}

var (
	toNetworkType = func(oldObj *api.NetworkProfile) *api.NetworkType { return &oldObj.NetworkType }
	toPodCIDR     = func(oldObj *api.NetworkProfile) *string { return &oldObj.PodCIDR }
	toServiceCIDR = func(oldObj *api.NetworkProfile) *string { return &oldObj.ServiceCIDR }
	toMachineCIDR = func(oldObj *api.NetworkProfile) *string { return &oldObj.MachineCIDR }
	toHostPrefix  = func(oldObj *api.NetworkProfile) *int32 { return &oldObj.HostPrefix }
)

// Network                 NetworkProfile              `json:"network,omitempty"                 visibility:"read create"`
func validateNetworkProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NetworkProfile) field.ErrorList {
	errs := field.ErrorList{}

	// NetworkType NetworkType `json:"networkType,omitempty" validate:"enum_networktype"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("networkType"), &newObj.NetworkType, safe.Field(oldObj, toNetworkType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("networkType"), &newObj.NetworkType, nil, api.ValidNetworkTypes)...)

	// PodCIDR     string      `json:"podCidr,omitempty"     validate:"omitempty,cidrv4"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("podCidr"), &newObj.PodCIDR, safe.Field(oldObj, toPodCIDR))...)
	errs = append(errs, CIDRv4(ctx, op, fldPath.Child("podCidr"), &newObj.PodCIDR, nil)...)

	// ServiceCIDR string      `json:"serviceCidr,omitempty" validate:"omitempty,cidrv4"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("serviceCidr"), &newObj.ServiceCIDR, safe.Field(oldObj, toServiceCIDR))...)
	errs = append(errs, CIDRv4(ctx, op, fldPath.Child("serviceCidr"), &newObj.ServiceCIDR, nil)...)

	// MachineCIDR string      `json:"machineCidr,omitempty" validate:"omitempty,cidrv4"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("machineCidr"), &newObj.MachineCIDR, safe.Field(oldObj, toMachineCIDR))...)
	errs = append(errs, CIDRv4(ctx, op, fldPath.Child("machineCidr"), &newObj.MachineCIDR, nil)...)

	// HostPrefix  int32       `json:"hostPrefix,omitempty"  validate:"omitempty,min=23,max=26"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("hostPrefix"), &newObj.HostPrefix, safe.Field(oldObj, toHostPrefix))...)
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("hostPrefix"), &newObj.HostPrefix, nil, 23)...)
	errs = append(errs, Maximum(ctx, op, fldPath.Child("hostPrefix"), &newObj.HostPrefix, nil, 26)...)

	// Just check for overlapping subnets. Defer subnet limits to Cluster Service.
	_, podCIDR, _ := net.ParseCIDR(newObj.PodCIDR)
	_, serviceCIDR, _ := net.ParseCIDR(newObj.ServiceCIDR)
	_, machineCIDR, _ := net.ParseCIDR(newObj.MachineCIDR)

	intersect := func(n1, n2 *net.IPNet) bool {
		if n1 == nil || n2 == nil {
			return false
		}

		return n2.Contains(n1.IP) || n1.Contains(n2.IP)
	}
	if intersect(machineCIDR, serviceCIDR) {
		errs = append(errs, field.Invalid(fldPath, newObj.MachineCIDR, fmt.Sprintf("machine CIDR '%s' and service CIDR '%s' overlap", newObj.MachineCIDR, newObj.ServiceCIDR)))
	}
	if intersect(machineCIDR, podCIDR) {
		errs = append(errs, field.Invalid(fldPath, newObj.MachineCIDR, fmt.Sprintf("machine CIDR '%s' and pod CIDR '%s' overlap", newObj.MachineCIDR, newObj.PodCIDR)))
	}
	if intersect(serviceCIDR, podCIDR) {
		errs = append(errs, field.Invalid(fldPath, newObj.ServiceCIDR, fmt.Sprintf("service CIDR '%s' and pod CIDR '%s' overlap", newObj.ServiceCIDR, newObj.PodCIDR)))
	}

	return errs
}

var (
	toConsoleURL = func(oldObj *api.ServiceProviderConsoleProfile) *string { return &oldObj.URL }
)

// Console                 ServiceProviderConsoleProfile              `json:"console,omitempty"                 visibility:"read"`
func validateServiceProviderConsoleProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ServiceProviderConsoleProfile) field.ErrorList {
	errs := field.ErrorList{}

	// URL string `json:"url,omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toConsoleURL))...)

	return errs
}

var (
	toAPIVisibility      = func(oldObj *api.CustomerAPIProfile) *api.Visibility { return &oldObj.Visibility }
	toAPIAuthorizedCIDRs = func(oldObj *api.CustomerAPIProfile) []string { return oldObj.AuthorizedCIDRs }
)

func validateCustomerAPIProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.CustomerAPIProfile) field.ErrorList {
	errs := field.ErrorList{}

	// Visibility      Visibility `json:"visibility,omitempty"      visibility:"read create"        validate:"enum_visibility"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("visiblity"), &newObj.Visibility, safe.Field(oldObj, toAPIVisibility))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("visiblity"), &newObj.Visibility, nil, api.ValidVisibility)...)

	// AuthorizedCIDRs []string   `json:"authorizedCidrs,omitempty" visibility:"read create update" validate:"max=500,dive,ipv4|cidrv4"`
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("authorizedCidrs"), newObj.AuthorizedCIDRs, nil, 500)...)
	errs = append(errs,
		validate.EachSliceVal(
			ctx, op, fldPath.Child("authorizedCidrs"),
			newObj.AuthorizedCIDRs, safe.Field(oldObj, toAPIAuthorizedCIDRs),
			nil, nil,
			newOr(IPv4, CIDRv4),
		)...)
	errs = append(errs,
		validate.EachSliceVal(
			ctx, op, fldPath.Child("authorizedCidrs"),
			newObj.AuthorizedCIDRs, safe.Field(oldObj, toAPIAuthorizedCIDRs),
			nil, nil,
			validate.RequiredValue,
		)...)
	errs = append(errs,
		validate.EachSliceVal(
			ctx, op, fldPath.Child("authorizedCidrs"),
			newObj.AuthorizedCIDRs, safe.Field(oldObj, toAPIAuthorizedCIDRs),
			nil, nil,
			NoExtraWhitespace,
		)...)

	return errs
}

var (
	toAPIURL = func(oldObj *api.ServiceProviderAPIProfile) *string { return &oldObj.URL }
)

func validateServiceProviderAPIProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ServiceProviderAPIProfile) field.ErrorList {
	errs := field.ErrorList{}

	// URL             string     `json:"url,omitempty"             visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toAPIURL))...)

	return errs
}

var (
	toPlatformManagedResourceGroup    = func(oldObj *api.CustomerPlatformProfile) *string { return &oldObj.ManagedResourceGroup }
	toPlatformSubnetID                = func(oldObj *api.CustomerPlatformProfile) *string { return &oldObj.SubnetID }
	toPlatformOutboundType            = func(oldObj *api.CustomerPlatformProfile) *api.OutboundType { return &oldObj.OutboundType }
	toPlatformNetworkSecurityGroupID  = func(oldObj *api.CustomerPlatformProfile) *string { return &oldObj.NetworkSecurityGroupID }
	toPlatformOperatorsAuthentication = func(oldObj *api.CustomerPlatformProfile) *api.OperatorsAuthenticationProfile {
		return &oldObj.OperatorsAuthentication
	}
)

// Platform                CustomerPlatformProfile             `json:"platform,omitempty"                visibility:"read create"`
func validateCustomerPlatformProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.CustomerPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	//ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("managedResourceGroup"), &newObj.ManagedResourceGroup, safe.Field(oldObj, toPlatformManagedResourceGroup))...)

	//SubnetID                string                         `json:"subnetId,omitempty"                                  validate:"required,resource_id=Microsoft.Network/virtualNetworks/subnets"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("subnetId"), &newObj.SubnetID, safe.Field(oldObj, toPlatformSubnetID))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("subnetId"), &newObj.SubnetID, safe.Field(oldObj, toPlatformSubnetID))...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, fldPath.Child("subnetId"), &newObj.SubnetID, nil, newObj.ManagedResourceGroup)...)

	//OutboundType            OutboundType                   `json:"outboundType,omitempty"                              validate:"enum_outboundtype"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("outboundType"), &newObj.OutboundType, safe.Field(oldObj, toPlatformOutboundType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("outboundType"), &newObj.OutboundType, nil, api.ValidOutboundTypes)...)

	//NetworkSecurityGroupID  string                         `json:"networkSecurityGroupId,omitempty"                    validate:"required,resource_id=Microsoft.Network/networkSecurityGroups"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("networkSecurityGroupId"), &newObj.NetworkSecurityGroupID, safe.Field(oldObj, toPlatformNetworkSecurityGroupID))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("networkSecurityGroupId"), &newObj.NetworkSecurityGroupID, safe.Field(oldObj, toPlatformNetworkSecurityGroupID))...)
	errs = append(errs, RestrictedResourceID(ctx, op, fldPath.Child("networkSecurityGroupId"), &newObj.NetworkSecurityGroupID, safe.Field(oldObj, toPlatformNetworkSecurityGroupID), "Microsoft.Network/networkSecurityGroups")...)

	//OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("operatorsAuthentication"), &newObj.OperatorsAuthentication, safe.Field(oldObj, toPlatformOperatorsAuthentication))...)
	errs = append(errs, validateOperatorsAuthenticationProfile(ctx, op, fldPath.Child("operatorsAuthentication"), &newObj.OperatorsAuthentication, safe.Field(oldObj, toPlatformOperatorsAuthentication))...)

	return errs
}

var (
	toServiceProviderPlatformProfileIssuerURL = func(oldObj *api.ServiceProviderPlatformProfile) *string { return &oldObj.IssuerURL }
)

// Platform                CustomerPlatformProfile             `json:"platform,omitempty"                visibility:"read create"`
func validateServiceProviderPlatformProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ServiceProviderPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	//IssuerURL               string                         `json:"issuerUrl,omitempty"               visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("issuerUrl"), &newObj.IssuerURL, safe.Field(oldObj, toServiceProviderPlatformProfileIssuerURL))...)

	return errs
}

var (
	toAuthenticationUserAssignedIdentities = func(oldObj *api.OperatorsAuthenticationProfile) *api.UserAssignedIdentitiesProfile {
		return &oldObj.UserAssignedIdentities
	}
)

func validateOperatorsAuthenticationProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.OperatorsAuthenticationProfile) field.ErrorList {
	errs := field.ErrorList{}

	//UserAssignedIdentities UserAssignedIdentitiesProfile `json:"userAssignedIdentities,omitempty"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("userAssignedIdentities"), &newObj.UserAssignedIdentities, safe.Field(oldObj, toAuthenticationUserAssignedIdentities))...)
	errs = append(errs, validateUserAssignedIdentitiesProfile(ctx, op, fldPath.Child("userAssignedIdentities"), &newObj.UserAssignedIdentities, safe.Field(oldObj, toAuthenticationUserAssignedIdentities))...)

	return errs
}

var (
	toUserAssignedIdentitiesControlPlaneOperators  = func(oldObj *api.UserAssignedIdentitiesProfile) map[string]string { return oldObj.ControlPlaneOperators }
	toUserAssignedIdentitiesDataPlaneOperators     = func(oldObj *api.UserAssignedIdentitiesProfile) map[string]string { return oldObj.DataPlaneOperators }
	toUserAssignedIdentitiesServiceManagedIdentity = func(oldObj *api.UserAssignedIdentitiesProfile) *string { return &oldObj.ServiceManagedIdentity }
)

func validateUserAssignedIdentitiesProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.UserAssignedIdentitiesProfile) field.ErrorList {
	errs := field.ErrorList{}

	//ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"  validate:"dive,keys,required,endkeys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("controlPlaneOperators"), newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators))...)
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("controlPlaneOperators"),
		newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators),
		validate.RequiredValue,
	)...)
	// even though it's not listed, prior validation had the value required.
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("controlPlaneOperators"),
		newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators),
		nil,
		validate.RequiredValue,
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("controlPlaneOperators"),
		newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators),
		nil,
		newRestrictedResourceID("Microsoft.ManagedIdentity/userAssignedIdentities"),
	)...)

	//DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"     validate:"dive,keys,required,endkeys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("dataPlaneOperators"), newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators))...)
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("dataPlaneOperators"),
		newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators),
		validate.RequiredValue,
	)...)
	// even though it's not listed, prior validation had the value required.
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("dataPlaneOperators"),
		newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators),
		nil,
		validate.RequiredValue,
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("dataPlaneOperators"),
		newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators),
		nil,
		newRestrictedResourceID("Microsoft.ManagedIdentity/userAssignedIdentities"),
	)...)

	//ServiceManagedIdentity string            `json:"serviceManagedIdentity,omitempty" validate:"omitempty,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("serviceManagedIdentity"), &newObj.ServiceManagedIdentity, safe.Field(oldObj, toUserAssignedIdentitiesServiceManagedIdentity))...)
	errs = append(errs, RestrictedResourceID(ctx, op, fldPath.Child("serviceManagedIdentity"), &newObj.ServiceManagedIdentity, safe.Field(oldObj, toUserAssignedIdentitiesServiceManagedIdentity), "Microsoft.ManagedIdentity/userAssignedIdentities")...)

	return errs
}

func validateClusterAutoscalingProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ClusterAutoscalingProfile) field.ErrorList {
	errs := field.ErrorList{}

	//MaxNodesTotal               int32 `json:"maxNodesTotal,omitempty"`
	//MaxPodGracePeriodSeconds    int32 `json:"maxPodGracePeriodSeconds,omitempty"`
	//MaxNodeProvisionTimeSeconds int32 `json:"maxNodeProvisionTimeSeconds,omitempty"`
	//PodPriorityThreshold        int32 `json:"podPriorityThreshold,omitempty"`

	return errs
}

var (
	toEtcdProfileDataEncryption = func(oldObj *api.EtcdProfile) *api.EtcdDataEncryptionProfile { return &oldObj.DataEncryption }
)

func validateEtcdProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.EtcdProfile) field.ErrorList {
	errs := field.ErrorList{}

	//DataEncryption EtcdDataEncryptionProfile `json:"dataEncryption,omitempty"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("dataEncryption"), &newObj.DataEncryption, safe.Field(oldObj, toEtcdProfileDataEncryption))...)
	errs = append(errs, validateEtcdDataEncryptionProfile(ctx, op, fldPath.Child("dataEncryption"), &newObj.DataEncryption, safe.Field(oldObj, toEtcdProfileDataEncryption))...)

	return errs
}

var (
	toEtcdDataEncryptionProfileKeyManagementMode = func(oldObj *api.EtcdDataEncryptionProfile) *api.EtcdDataEncryptionKeyManagementModeType {
		return &oldObj.KeyManagementMode
	}
	toEtcdDataEncryptionProfileCustomerManaged = func(oldObj *api.EtcdDataEncryptionProfile) *api.CustomerManagedEncryptionProfile {
		return oldObj.CustomerManaged
	}
)

func validateEtcdDataEncryptionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.EtcdDataEncryptionProfile) field.ErrorList {
	errs := field.ErrorList{}

	//KeyManagementMode EtcdDataEncryptionKeyManagementModeType `json:"keyManagementMode,omitempty" validate:"enum_etcddataencryptionkeymanagementmodetype"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("keyManagementMode"), &newObj.KeyManagementMode, safe.Field(oldObj, toEtcdDataEncryptionProfileKeyManagementMode))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("keyManagementMode"), &newObj.KeyManagementMode, safe.Field(oldObj, toEtcdDataEncryptionProfileKeyManagementMode), api.ValidEtcdDataEncryptionKeyManagementModeType)...)

	//CustomerManaged   *CustomerManagedEncryptionProfile       `json:"customerManaged,omitempty"   validate:"required_if=KeyManagementMode CustomerManaged,excluded_unless=KeyManagementMode CustomerManaged,omitempty"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("customerManaged"), newObj.CustomerManaged, safe.Field(oldObj, toEtcdDataEncryptionProfileCustomerManaged))...)
	union := validate.NewDiscriminatedUnionMembership("keyManagementMode", [2]string{"customerManaged", "CustomerManaged"})
	discriminatorExtractor := func(obj *api.EtcdDataEncryptionProfile) api.EtcdDataEncryptionKeyManagementModeType {
		return obj.KeyManagementMode
	}
	isCustomerManagedSetFn := func(obj *api.EtcdDataEncryptionProfile) bool {
		return obj.CustomerManaged != nil
	}
	// this verifies that CustomerManaged is set iff keyManagementMode==CustomerManaged
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isCustomerManagedSetFn)...)
	errs = append(errs, validateCustomerManagedEncryptionProfile(ctx, op, fldPath.Child("customerManaged"), newObj.CustomerManaged, safe.Field(oldObj, toEtcdDataEncryptionProfileCustomerManaged))...)

	return errs
}

var (
	toCustomerManagedEncryptionProfileEncryptionType = func(oldObj *api.CustomerManagedEncryptionProfile) *api.CustomerManagedEncryptionType {
		return &oldObj.EncryptionType
	}
	toEtcdDataEncryptionProfileKms = func(oldObj *api.CustomerManagedEncryptionProfile) *api.KmsEncryptionProfile { return oldObj.Kms }
)

func validateCustomerManagedEncryptionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.CustomerManagedEncryptionProfile) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//EncryptionType CustomerManagedEncryptionType `json:"encryptionType,omitempty" validate:"enum_customermanagedencryptiontype"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("encryptionType"), &newObj.EncryptionType, safe.Field(oldObj, toCustomerManagedEncryptionProfileEncryptionType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("encryptionType"), &newObj.EncryptionType, safe.Field(oldObj, toCustomerManagedEncryptionProfileEncryptionType), api.ValidCustomerManagedEncryptionType)...)

	//Kms            *KmsEncryptionProfile         `json:"kms,omitempty"            validate:"required_if=EncryptionType KMS,excluded_unless=EncryptionType KMS,omitempty"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("kms"), newObj.Kms, safe.Field(oldObj, toEtcdDataEncryptionProfileKms))...)
	union := validate.NewDiscriminatedUnionMembership("encryptionType", [2]string{"kms", "KMS"})
	discriminatorExtractor := func(obj *api.CustomerManagedEncryptionProfile) api.CustomerManagedEncryptionType {
		return obj.EncryptionType
	}
	isCustomerManagedSetFn := func(obj *api.CustomerManagedEncryptionProfile) bool {
		return obj.Kms != nil
	}
	// this verifies that Kms is set iff encryptionType==KMS
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isCustomerManagedSetFn)...)
	errs = append(errs, validateKmsEncryptionProfile(ctx, op, fldPath.Child("kms"), newObj.Kms, safe.Field(oldObj, toEtcdDataEncryptionProfileKms))...)

	return errs
}

var (
	toKmsEncryptionProfileActiveKey = func(oldObj *api.KmsEncryptionProfile) *api.KmsKey { return &oldObj.ActiveKey }
)

func validateKmsEncryptionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.KmsEncryptionProfile) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//ActiveKey KmsKey `json:"activeKey,omitempty"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("activeKey"), &newObj.ActiveKey, safe.Field(oldObj, toKmsEncryptionProfileActiveKey))...)
	errs = append(errs, validateKmsKey(ctx, op, fldPath.Child("activeKey"), &newObj.ActiveKey, safe.Field(oldObj, toKmsEncryptionProfileActiveKey))...)

	return errs
}

var (
	toKmsKeyName      = func(oldObj *api.KmsKey) *string { return &oldObj.Name }
	toKmsKeyVaultName = func(oldObj *api.KmsKey) *string { return &oldObj.VaultName }
	toKmsKeyVersion   = func(oldObj *api.KmsKey) *string { return &oldObj.Version }
)

func validateKmsKey(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.KmsKey) field.ErrorList {
	errs := field.ErrorList{}

	//Name      string `json:"name"      validate:"required,min=1,max=255"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toKmsKeyName))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("name"), &newObj.Name, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("name"), &newObj.Name, nil, 255)...)

	//VaultName string `json:"vaultName" validate:"required,min=1,max=255"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("vaultName"), &newObj.VaultName, safe.Field(oldObj, toKmsKeyVaultName))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("vaultName"), &newObj.VaultName, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("vaultName"), &newObj.VaultName, nil, 255)...)

	//Version   string `json:"version"   validate:"required,min=1,max=255"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, toKmsKeyVersion))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("version"), &newObj.Version, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("version"), &newObj.Version, nil, 255)...)

	return errs
}

var (
	toPlatformClusterImageRegistryState = func(oldObj *api.ClusterImageRegistryProfile) *api.ClusterImageRegistryProfileState {
		return &oldObj.State
	}
)

func validateClusterImageRegistryProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.ClusterImageRegistryProfile) field.ErrorList {
	errs := field.ErrorList{}

	//State ClusterImageRegistryProfileState `json:"state,omitempty" validate:"enum_clusterimageregistryprofilestate"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("state"), &newObj.State, safe.Field(oldObj, toPlatformClusterImageRegistryState))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("state"), &newObj.State, safe.Field(oldObj, toPlatformClusterImageRegistryState), api.ValidClusterImageRegistryProfileStates)...)

	return errs
}

var (
	toManagedServiceIdentityPrincipalID            = func(oldObj *arm.ManagedServiceIdentity) *string { return &oldObj.PrincipalID }
	toManagedServiceIdentityTenantID               = func(oldObj *arm.ManagedServiceIdentity) *string { return &oldObj.TenantID }
	toManagedServiceIdentityType                   = func(oldObj *arm.ManagedServiceIdentity) *arm.ManagedServiceIdentityType { return &oldObj.Type }
	toManagedServiceIdentityUserAssignedIdentities = func(oldObj *arm.ManagedServiceIdentity) map[string]*arm.UserAssignedIdentity {
		return oldObj.UserAssignedIdentities
	}
)

func validateManagedServiceIdentity(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *arm.ManagedServiceIdentity) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//PrincipalID            string                           `json:"principalId,omitempty"            visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("principalId"), &newObj.PrincipalID, safe.Field(oldObj, toManagedServiceIdentityPrincipalID))...)
	//TenantID               string                           `json:"tenantId,omitempty"               visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("tenantId"), &newObj.TenantID, safe.Field(oldObj, toManagedServiceIdentityTenantID))...)

	//Type                   ManagedServiceIdentityType       `json:"type"                                               validate:"required,enum_managedserviceidentitytype"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("type"), &newObj.Type, nil)...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("state"), &newObj.Type, safe.Field(oldObj, toManagedServiceIdentityType), arm.ValidManagedServiceIdentityTypes)...)

	//UserAssignedIdentities map[string]*UserAssignedIdentity `json:"userAssignedIdentities,omitempty"                   validate:"dive,keys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities,endkeys"`
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("userAssignedIdentities"),
		newObj.UserAssignedIdentities, safe.Field(oldObj, toManagedServiceIdentityUserAssignedIdentities),
		validate.RequiredValue,
	)...)
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("userAssignedIdentities"),
		newObj.UserAssignedIdentities, safe.Field(oldObj, toManagedServiceIdentityUserAssignedIdentities),
		newRestrictedResourceID("Microsoft.ManagedIdentity/userAssignedIdentities"),
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("userAssignedIdentities"),
		newObj.UserAssignedIdentities, safe.Field(oldObj, toManagedServiceIdentityUserAssignedIdentities),
		nil,
		validateUserAssignedIdentity,
	)...)

	return errs
}

var (
	toUserAssignedIdentityClientID = func(oldObj **arm.UserAssignedIdentity) *string {
		if oldObj == nil || *oldObj == nil {
			return nil
		}
		return (*oldObj).ClientID
	}
	toUserAssignedIdentityPrincipalID = func(oldObj **arm.UserAssignedIdentity) *string {
		if oldObj == nil || *oldObj == nil {
			return nil
		}
		return (*oldObj).PrincipalID
	}
)

func validateUserAssignedIdentity(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj **arm.UserAssignedIdentity) field.ErrorList {
	if newObj == nil || *newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//ClientID    *string `json:"clientId,omitempty"    visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("clientId"), (*newObj).ClientID, safe.Field(oldObj, toUserAssignedIdentityClientID))...)

	//PrincipalID *string `json:"principalId,omitempty" visibility:"read"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("principalId"), (*newObj).PrincipalID, safe.Field(oldObj, toUserAssignedIdentityPrincipalID))...)

	return errs
}
