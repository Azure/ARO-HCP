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
	"maps"
	"net"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	// ManagedIdentitiesDataPlaneIdentityURLOptionalOperationOption is an operation option that indicates that the managed identities
	// data plane identity URL is optional during validation. This is used on ARM Preflight requests on the
	// Cluster resource.
	ManagedIdentitiesDataPlaneIdentityURLOptionalOperationOption = "ManagedIdentitiesDataPlaneIdentityURLOptional"
)

// ToClusterTrackedResource returns a pointer to the TrackedResource field of
// a cluster. Exported so admission can traverse the same field path.
func ToClusterTrackedResource(oldObj *coreapi.HCPOpenShiftCluster) *coreapi.TrackedResource {
	return &oldObj.TrackedResource
}

// ToClusterCustomerProperties returns a pointer to the CustomerProperties
// field of a cluster. Exported so admission can traverse the same field path.
func ToClusterCustomerProperties(oldObj *coreapi.HCPOpenShiftCluster) *coreapi.HCPOpenShiftClusterCustomerProperties {
	return &oldObj.CustomerProperties
}

// ToClusterServiceProviderProperties returns a pointer to the
// ServiceProviderProperties field of a cluster. Exported so admission can
// traverse the same field path.
func ToClusterServiceProviderProperties(oldObj *coreapi.HCPOpenShiftCluster) *coreapi.HCPOpenShiftClusterServiceProviderProperties {
	return &oldObj.ServiceProviderProperties
}

var (
	toClusterIdentity = func(oldObj *coreapi.HCPOpenShiftCluster) *coreapi.ManagedServiceIdentity { return oldObj.Identity }
)

func ValidateCluster(ctx context.Context, op operation.Operation, newCluster, oldCluster *coreapi.HCPOpenShiftCluster, validationPathMapper coreapi.ValidationPathMapperFunc) field.ErrorList {
	errs := field.ErrorList{}

	//coreapi.TrackedResource
	errs = append(errs, validateTrackedResource(ctx, op, field.NewPath("trackedResource"), &newCluster.TrackedResource, safe.Field(oldCluster, ToClusterTrackedResource))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, field.NewPath("id"), newCluster.ID, nil, coreapi.ClusterResourceType.String())...)
	if newCluster.ID != nil {
		errs = append(errs, MaxLen(ctx, op, field.NewPath("id"), &newCluster.ID.Name, nil, 54)...)
		errs = append(errs, MatchesRegex(ctx, op, field.NewPath("id"), &newCluster.ID.Name, nil, clusterResourceNameRegex, clusterResourceNameErrorString)...)
	}

	// Properties HCPOpenShiftClusterCustomerProperties `json:"properties,omitempty"`
	errs = append(errs, validateClusterCustomerProperties(ctx, op, field.NewPath("customerProperties"), &newCluster.CustomerProperties, safe.Field(oldCluster, ToClusterCustomerProperties))...)

	// Properties HCPOpenShiftClusterCustomerProperties `json:"properties,omitempty"`
	errs = append(errs, validateClusterServiceProviderProperties(ctx, op, field.NewPath("serviceProviderProperties"), &newCluster.ServiceProviderProperties, safe.Field(oldCluster, ToClusterServiceProviderProperties))...)

	// Identity   *coreapi.ManagedServiceIdentity   `json:"identity,omitempty"`
	errs = append(errs, validateManagedServiceIdentity(ctx, op, field.NewPath("identity"), newCluster.Identity, safe.Field(oldCluster, toClusterIdentity))...)

	// there several resourceIDs that must be verified with respect to this ID.  This is the only level of validation with access to both
	errs = append(errs, validateResourceIDsAgainstClusterID(ctx, op, newCluster, oldCluster)...)

	// Private KAS requires vnetIntegrationSubnetId to be set.
	// For API versions v20251223preview and later, vnetIntegrationSubnetId is already
	// enforced as required during conversion, so this check only has practical effect
	// for v20240610preview.
	errs = append(errs, validatePrivateKASRequiresVNetIntegrationSubnetID(ctx, op, newCluster, oldCluster)...)

	// Private KAS requires OpenShift >= 4.22 (HyperShift gained private API server support in 4.22).
	errs = append(errs, validatePrivateKASRequiresMinimumVersion(ctx, op, newCluster, oldCluster)...)

	// there are pieces of clusterProperties that are dependent upon values in .identity
	errs = append(errs, validateOperatorAuthenticationAgainstIdentities(ctx, op, newCluster, oldCluster)...)

	RewriteValidationFieldPaths(errs, validationPathMapper)

	return errs
}

func validatePrivateKASRequiresVNetIntegrationSubnetID(_ context.Context, _ operation.Operation, newCluster, _ *coreapi.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	if newCluster.CustomerProperties.API.Visibility == metadataapi.VisibilityPrivate &&
		newCluster.CustomerProperties.Platform.VnetIntegrationSubnetID == nil {
		errs = append(errs, field.Required(
			field.NewPath("customerProperties", "platform", "vnetIntegrationSubnetId"),
			"required when customerProperties.api.visibility is Private",
		))
	}

	return errs
}

// minPrivateKASOpenShiftVersion is the minimum OCP version that supports
// private KAS (API server) visibility.
var minPrivateKASOpenShiftVersion = semver.Version{Major: 4, Minor: 22}

// validatePrivateKASRequiresMinimumVersion rejects Private API visibility when
// the requested OpenShift version is below 4.22.
func validatePrivateKASRequiresMinimumVersion(_ context.Context, _ operation.Operation, newCluster, _ *coreapi.HCPOpenShiftCluster) field.ErrorList {
	if newCluster.CustomerProperties.API.Visibility != metadataapi.VisibilityPrivate {
		return nil
	}

	versionID := newCluster.CustomerProperties.Version.ID
	if len(versionID) == 0 {
		return nil
	}

	requestedVersion, err := semver.ParseTolerant(versionID)
	if err != nil {
		// Other validators will report the parse failure; skip here to
		// avoid duplicate errors.
		return nil
	}

	clusterVersion := semver.Version{Major: requestedVersion.Major, Minor: requestedVersion.Minor}
	if clusterVersion.LT(minPrivateKASOpenShiftVersion) {
		return field.ErrorList{field.Invalid(
			field.NewPath("customerProperties", "api", "visibility"),
			string(newCluster.CustomerProperties.API.Visibility),
			fmt.Sprintf("not supported when customerProperties.version.id is below %d.%d", minPrivateKASOpenShiftVersion.Major, minPrivateKASOpenShiftVersion.Minor),
		)}
	}

	return nil
}

func validateOperatorAuthenticationAgainstIdentities(ctx context.Context, op operation.Operation, newCluster, _ *coreapi.HCPOpenShiftCluster) field.ErrorList {
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

	tallyIdentity := func(identity *azcorearm.ResourceID, fldPath *field.Path) {
		if identity == nil {
			return
		}
		key := strings.ToLower(identity.String())
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

	if serviceManagedIdentity := newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity; serviceManagedIdentity != nil {
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
		if operatorIdentity == nil {
			// other validation fails on this being nil
			continue
		}
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "dataPlaneOperators").Key(operatorName)
		key := strings.ToLower(operatorIdentity.String())
		if _, ok := userAssignedIdentities[key]; ok {
			errs = append(errs, field.Invalid(fldPath, operatorIdentity, "cannot use identity assigned to this resource by .identities.userAssignedIdentities"))
		}
	}

	return errs
}

func validateResourceIDsAgainstClusterID(ctx context.Context, op operation.Operation, newCluster, _ *coreapi.HCPOpenShiftCluster) field.ErrorList {
	if newCluster.ID == nil {
		return nil
	}

	errs := field.ErrorList{}

	// Validate that managed resource group is different from cluster resource group
	errs = append(errs, DifferentResourceGroupName(ctx, op, field.NewPath("customerProperties", "platform", "managedResourceGroup"), &newCluster.CustomerProperties.Platform.ManagedResourceGroup, nil, newCluster.ID.ResourceGroupName)...)
	// NOTE: Our admission logic expects that the subnet and network security group are in the same subscription as the cluster.
	// If these validations are removed, the admission logic should also be updated.
	errs = append(errs, SameSubscription(ctx, op, field.NewPath("customerProperties", "platform", "subnetId"), newCluster.CustomerProperties.Platform.SubnetID, nil, newCluster.ID.SubscriptionID)...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, field.NewPath("customerProperties", "platform", "subnetId"), newCluster.CustomerProperties.Platform.SubnetID, nil, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)
	errs = append(errs, SameSubscription(ctx, op, field.NewPath("customerProperties", "platform", "networkSecurityGroupId"), newCluster.CustomerProperties.Platform.NetworkSecurityGroupID, nil, newCluster.ID.SubscriptionID)...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, field.NewPath("customerProperties", "platform", "networkSecurityGroupId"), newCluster.CustomerProperties.Platform.NetworkSecurityGroupID, nil, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)
	errs = append(errs, SameSubscription(ctx, op, field.NewPath("customerProperties", "platform", "vnetIntegrationSubnetId"), newCluster.CustomerProperties.Platform.VnetIntegrationSubnetID, nil, newCluster.ID.SubscriptionID)...)
	errs = append(errs, SameSubscription(ctx, op, field.NewPath("customerProperties", "platform", "containerRegistry", "managedIdentity"), newCluster.CustomerProperties.Platform.ContainerRegistry.PullManagedIdentity, nil, newCluster.ID.SubscriptionID)...)

	for operatorName, operatorIdentity := range newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "controlPlaneOperators").Key(operatorName)
		errs = append(errs, ValidateUserAssignedIdentityLocation(ctx, op, fldPath, operatorIdentity, nil, newCluster.ID.SubscriptionID, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)
	}
	for operatorName, operatorIdentity := range newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		fldPath := field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "dataPlaneOperators").Key(operatorName)
		errs = append(errs, ValidateUserAssignedIdentityLocation(ctx, op, fldPath, operatorIdentity, nil, newCluster.ID.SubscriptionID, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)
	}
	errs = append(errs, ValidateUserAssignedIdentityLocation(ctx, op,
		field.NewPath("customerProperties", "platform", "operatorsAuthentication", "userAssignedIdentities", "serviceManagedIdentity"),
		newCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity, nil,
		newCluster.ID.SubscriptionID, newCluster.CustomerProperties.Platform.ManagedResourceGroup)...)

	return errs
}

// ToClusterCustomerPropertiesVersion returns a pointer to the Version field
// of cluster customer properties. Exported so admission can traverse the same
// field path.
func ToClusterCustomerPropertiesVersion(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.VersionProfile {
	return &oldObj.Version
}

var (
	toCustomerDNS = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.CustomerDNSProfile {
		return &oldObj.DNS
	}
	toNetwork = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.NetworkProfile {
		return &oldObj.Network
	}
	toCustomerAPI = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.CustomerAPIProfile {
		return &oldObj.API
	}
	toCustomerIngress = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.CustomerIngressProfile {
		return &oldObj.Ingress
	}
	toCustomerPlatform = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.CustomerPlatformProfile {
		return &oldObj.Platform
	}
	toClusterAutoscaling = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.ClusterAutoscalingProfile {
		return &oldObj.Autoscaling
	}
	toNodeDrainTimeoutMinutes = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *int32 {
		return &oldObj.NodeDrainTimeoutMinutes
	}
	toEtcd                 = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.EtcdProfile { return &oldObj.Etcd }
	toClusterImageRegistry = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *coreapi.ClusterImageRegistryProfile {
		return &oldObj.ClusterImageRegistry
	}
	toImageDigestMirrors = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) []coreapi.ImageDigestMirror {
		return oldObj.ImageDigestMirrors
	}
	toCryptoRestrictions = func(oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) *metadataapi.CryptoRestrictions {
		return &oldObj.CryptoRestrictions
	}
)

func validateClusterCustomerProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) field.ErrorList {
	errs := field.ErrorList{}

	// Version                 VersionProfile              `json:"version,omitempty"`
	errs = append(errs, validateVersionProfile(ctx, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, ToClusterCustomerPropertiesVersion))...)

	// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
	errs = append(errs, validateCustomerDNSProfile(ctx, op, fldPath.Child("dns"), &newObj.DNS, safe.Field(oldObj, toCustomerDNS))...)

	// Network                 NetworkProfile              `json:"network,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("network"), &newObj.Network, safe.Field(oldObj, toNetwork))...)
	errs = append(errs, validateNetworkProfile(ctx, op, fldPath.Child("network"), &newObj.Network, safe.Field(oldObj, toNetwork))...)

	// API                     CustomerAPIProfile                  `json:"api,omitempty"`
	errs = append(errs, validateCustomerAPIProfile(ctx, op, fldPath.Child("api"), &newObj.API, safe.Field(oldObj, toCustomerAPI))...)

	// Ingress                 CustomerIngressProfile              `json:"ingress,omitempty"`
	errs = append(errs, validateCustomerIngressProfile(ctx, op, fldPath.Child("ingress"), &newObj.Ingress, safe.Field(oldObj, toCustomerIngress))...)

	// Platform                CustomerPlatformProfile             `json:"platform,omitempty"`
	errs = append(errs, validateCustomerPlatformProfile(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toCustomerPlatform))...)

	//Autoscaling             ClusterAutoscalingProfile   `json:"autoscaling,omitempty"`
	errs = append(errs, validateClusterAutoscalingProfile(ctx, op, fldPath.Child("autoscaling"), &newObj.Autoscaling, safe.Field(oldObj, toClusterAutoscaling))...)

	//NodeDrainTimeoutMinutes int32                       `json:"nodeDrainTimeoutMinutes,omitempty"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("nodeDrainTimeoutMinutes"), &newObj.NodeDrainTimeoutMinutes, safe.Field(oldObj, toNodeDrainTimeoutMinutes), 0)...)
	errs = append(errs, Maximum(ctx, op, fldPath.Child("nodeDrainTimeoutMinutes"), &newObj.NodeDrainTimeoutMinutes, safe.Field(oldObj, toNodeDrainTimeoutMinutes), 10080)...)

	//Etcd                    EtcdProfile                 `json:"etcd,omitempty"`
	errs = append(errs, validateEtcdProfile(ctx, op, fldPath.Child("etcd"), &newObj.Etcd, safe.Field(oldObj, toEtcd))...)

	//ClusterImageRegistry    ClusterImageRegistryProfile `json:"clusterImageRegistry,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("clusterImageRegistry"), &newObj.ClusterImageRegistry, safe.Field(oldObj, toClusterImageRegistry))...)
	errs = append(errs, validateClusterImageRegistryProfile(ctx, op, fldPath.Child("clusterImageRegistry"), &newObj.ClusterImageRegistry, safe.Field(oldObj, toClusterImageRegistry))...)

	//ImageDigestMirrors  []ImageDigestMirror `json:"imageDigestMirrors,omitempty"`
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("imageDigestMirrors"), newObj.ImageDigestMirrors, safe.Field(oldObj, toImageDigestMirrors), 240)...)
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("imageDigestMirrors"),
		newObj.ImageDigestMirrors, safe.Field(oldObj, toImageDigestMirrors),
		nil, nil,
		validateImageDigestMirror,
	)...)

	// CryptoRestrictions
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("cryptoRestrictions"), &newObj.CryptoRestrictions, safe.Field(oldObj, toCryptoRestrictions))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("cryptoRestrictions"), &newObj.CryptoRestrictions, nil, metadataapi.ValidCryptoRestrictions, nil)...)

	return errs
}

var (
	toHCPOpenShiftClusterServiceProviderPropertiesProvisioningState = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *coreapi.ProvisioningState {
		return &oldObj.ProvisioningState
	}
	toServiceProviderDNS = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *coreapi.ServiceProviderDNSProfile {
		return &oldObj.DNS
	}
	toServiceProviderClusterServiceID = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *metadataapi.InternalID {
		return oldObj.ClusterServiceID
	}
	toServiceProviderConsole = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *coreapi.ServiceProviderConsoleProfile {
		return &oldObj.Console
	}
	toServiceProviderAPI = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *coreapi.ServiceProviderAPIProfile {
		return &oldObj.API
	}
	toServiceProviderPlatform = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *coreapi.ServiceProviderPlatformProfile {
		return &oldObj.Platform
	}
	toServiceProviderManagedIdentitiesDataPlaneIdentityURL = func(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *string {
		return &oldObj.ManagedIdentitiesDataPlaneIdentityURL
	}
)

// ToClusterServiceProviderPropertiesClusterUID returns a pointer to the
// ClusterUID field of cluster service provider properties. Exported so
// admission can traverse the same field path.
func ToClusterServiceProviderPropertiesClusterUID(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *string {
	return &oldObj.ClusterUID
}

func validateClusterServiceProviderProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	// ProvisioningState       coreapi.ProvisioningState       `json:"provisioningState,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("provisioningState"), &newObj.ProvisioningState, safe.Field(oldObj, toHCPOpenShiftClusterServiceProviderPropertiesProvisioningState))...)

	//ClusterServiceID  *InternalID                    `json:"clusterServiceID,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("clusterServiceID"), newObj.ClusterServiceID, safe.Field(oldObj, toServiceProviderClusterServiceID))...)

	// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
	errs = append(errs, validateServiceProviderDNSProfile(ctx, op, fldPath.Child("dns"), &newObj.DNS, safe.Field(oldObj, toServiceProviderDNS))...)

	// Console                 ServiceProviderConsoleProfile              `json:"console,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("console"), &newObj.Console, safe.Field(oldObj, toServiceProviderConsole))...)
	errs = append(errs, validateServiceProviderConsoleProfile(ctx, op, fldPath.Child("console"), &newObj.Console, safe.Field(oldObj, toServiceProviderConsole))...)

	// API                     CustomerAPIProfile                  `json:"api,omitempty"`
	errs = append(errs, validateServiceProviderAPIProfile(ctx, op, fldPath.Child("api"), &newObj.API, safe.Field(oldObj, toServiceProviderAPI))...)

	// Platform                CustomerPlatformProfile             `json:"platform,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toServiceProviderPlatform))...)
	errs = append(errs, validateServiceProviderPlatformProfile(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toServiceProviderPlatform))...)

	// ManagedIdentitiesDataPlaneIdentityURL  string  `json:"managedIdentitiesDataPlaneIdentityURL,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("managedIdentitiesDataPlaneIdentityURL"), &newObj.ManagedIdentitiesDataPlaneIdentityURL, safe.Field(oldObj, toServiceProviderManagedIdentitiesDataPlaneIdentityURL))...)
	// At the moment of introducing this field not all existing clusters have this field migrated yet, so we guard
	// the requirement of setting it only when it's already been set beforehand.
	if oldObj == nil || oldObj.ManagedIdentitiesDataPlaneIdentityURL != "" {
		if !op.HasOption(ManagedIdentitiesDataPlaneIdentityURLOptionalOperationOption) {
			errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("managedIdentitiesDataPlaneIdentityURL"), &newObj.ManagedIdentitiesDataPlaneIdentityURL, nil)...)
		}
	}
	// We can validate URL unconditionally because the URL validator accepts an empty string as the URL
	errs = append(errs, URL(ctx, op, fldPath.Child("managedIdentitiesDataPlaneIdentityURL"), &newObj.ManagedIdentitiesDataPlaneIdentityURL, nil)...)

	// ClusterUID      string                         `json:"clusterUID,omitempty"`
	// ClusterUID is always generated server-side by admission.MutateCluster on CREATE.
	// Both preflight and real cluster creation call admission before validation.
	if op.Type == operation.Create {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("clusterUID"), &newObj.ClusterUID, nil)...)
	}
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("clusterUID"), &newObj.ClusterUID, safe.Field(oldObj, ToClusterServiceProviderPropertiesClusterUID))...)

	return errs
}

var (
	toVersionID = func(oldObj *coreapi.VersionProfile) *string { return &oldObj.ID }
	//	toChannelGroup = func(oldObj *coreapi.VersionProfile) *string { return &oldObj.ChannelGroup }
)

// Version                 VersionProfile              `json:"version,omitempty"`
func validateVersionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.VersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	// Version should be immutable once is created.
	// ID           string `json:"id,omitempty"
	// Version ID is required, but some records may not have had it originally, so don't fail them yet.
	if oldObj == nil || len(oldObj.ID) > 0 {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("id"), &newObj.ID, nil)...)

		errs = append(errs, VersionMustBeAtLeast(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toVersionID), "4.20")...)

		errs = append(errs, VersionMayNotDecrease(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toVersionID))...)
		errs = append(errs, OpenshiftVersionAtMostOneMinorSkewWithField(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toVersionID))...)
	}
	if !op.HasOption(metadataapi.FeatureExperimentalReleaseFeatures) {
		// we never allow micro to any cluster that might live longer than a couple days.  We cannot allow it because it might install naughty things
		errs = append(errs, OpenshiftVersionWithoutMicro(ctx, op, fldPath.Child("id"), &newObj.ID, nil)...)
		// only allow OpenShift v5 and above for subscriptions that have the experimental feature registered for now
		// remove this together with the matching check in the control plane desired version controller when we are ready
		if v, err := semver.ParseTolerant(newObj.ID); err == nil && v.Major >= 5 {
			errs = append(errs, field.Invalid(fldPath.Child("id"), newObj.ID, "OpenShift v5 and above is not supported"))
		}
	} else {
		// For our CI clusters, let us install anything: allow full semver format (X.Y.Z-prerelease)
		errs = append(errs, OpenshiftVersionWithOptionalMicro(ctx, op, fldPath.Child("id"), &newObj.ID, nil)...)
	}

	// ChannelGroup string `json:"channelGroup,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, nil)...)

	if !op.HasOption(metadataapi.FeatureExperimentalReleaseFeatures) {
		// Without feature flag: "candidate" and "nightly" aren't allowed.
		errs = append(errs, validate.Enum(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, nil, metadataapi.AllowedChannelGroups, nil)...)
	} else {
		// TODO I think everyone should be able to do this, but we'll need to notify first
		errs = append(errs, validate.Enum(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, nil, metadataapi.AllowedChannelGroupsWithExperimentalFlag, nil)...)
	}

	return errs
}

var (
	toDNSBaseDomainPrefix = func(oldObj *coreapi.CustomerDNSProfile) *string { return &oldObj.BaseDomainPrefix }
)

// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
func validateCustomerDNSProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.CustomerDNSProfile) field.ErrorList {
	errs := field.ErrorList{}

	// BaseDomainPrefix string `json:"baseDomainPrefix,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("baseDomainPrefix"), &newObj.BaseDomainPrefix, safe.Field(oldObj, toDNSBaseDomainPrefix))...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("baseDomainPrefix"), &newObj.BaseDomainPrefix, nil, 15)...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("baseDomainPrefix"), &newObj.BaseDomainPrefix, nil, rfc1035LabelRegex, rfc1035ErrorString)...)

	return errs
}

var (
	toDNSBaseDomain = func(oldObj *coreapi.ServiceProviderDNSProfile) *string { return &oldObj.BaseDomain }
)

// DNS                     CustomerDNSProfile                  `json:"dns,omitempty"`
func validateServiceProviderDNSProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ServiceProviderDNSProfile) field.ErrorList {
	errs := field.ErrorList{}

	// BaseDomain       string `json:"baseDomain,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("baseDomain"), &newObj.BaseDomain, safe.Field(oldObj, toDNSBaseDomain))...)

	return errs
}

var (
	toNetworkType = func(oldObj *coreapi.NetworkProfile) *metadataapi.NetworkType { return &oldObj.NetworkType }
	toPodCIDR     = func(oldObj *coreapi.NetworkProfile) *string { return &oldObj.PodCIDR }
	toServiceCIDR = func(oldObj *coreapi.NetworkProfile) *string { return &oldObj.ServiceCIDR }
	toMachineCIDR = func(oldObj *coreapi.NetworkProfile) *string { return &oldObj.MachineCIDR }
	toHostPrefix  = func(oldObj *coreapi.NetworkProfile) *int32 { return &oldObj.HostPrefix }
)

// Network                 NetworkProfile              `json:"network,omitempty"`
func validateNetworkProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NetworkProfile) field.ErrorList {
	errs := field.ErrorList{}

	// NetworkType NetworkType `json:"networkType,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("networkType"), &newObj.NetworkType, safe.Field(oldObj, toNetworkType))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("networkType"), &newObj.NetworkType, safe.Field(oldObj, toNetworkType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("networkType"), &newObj.NetworkType, nil, metadataapi.ValidNetworkTypes, nil)...)

	// PodCIDR     string      `json:"podCidr,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("podCidr"), &newObj.PodCIDR, safe.Field(oldObj, toPodCIDR))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("podCidr"), &newObj.PodCIDR, safe.Field(oldObj, toPodCIDR))...)
	errs = append(errs, CIDRv4(ctx, op, fldPath.Child("podCidr"), &newObj.PodCIDR, nil)...)

	// ServiceCIDR string      `json:"serviceCidr,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("serviceCidr"), &newObj.ServiceCIDR, safe.Field(oldObj, toServiceCIDR))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("serviceCidr"), &newObj.ServiceCIDR, safe.Field(oldObj, toServiceCIDR))...)
	errs = append(errs, CIDRv4(ctx, op, fldPath.Child("serviceCidr"), &newObj.ServiceCIDR, nil)...)

	// MachineCIDR string      `json:"machineCidr,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("machineCidr"), &newObj.MachineCIDR, safe.Field(oldObj, toMachineCIDR))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("machineCidr"), &newObj.MachineCIDR, safe.Field(oldObj, toMachineCIDR))...)
	errs = append(errs, CIDRv4(ctx, op, fldPath.Child("machineCidr"), &newObj.MachineCIDR, nil)...)

	// HostPrefix  int32       `json:"hostPrefix,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("hostPrefix"), &newObj.HostPrefix, safe.Field(oldObj, toHostPrefix))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("hostPrefix"), &newObj.HostPrefix, safe.Field(oldObj, toHostPrefix))...)
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
	toConsoleURL = func(oldObj *coreapi.ServiceProviderConsoleProfile) *string { return &oldObj.URL }
)

// Console                 ServiceProviderConsoleProfile              `json:"console,omitempty"`
func validateServiceProviderConsoleProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ServiceProviderConsoleProfile) field.ErrorList {
	errs := field.ErrorList{}

	// URL string `json:"url,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toConsoleURL))...)

	return errs
}

var (
	toAPIVisibility      = func(oldObj *coreapi.CustomerAPIProfile) *metadataapi.Visibility { return &oldObj.Visibility }
	toAPIAuthorizedCIDRs = func(oldObj *coreapi.CustomerAPIProfile) []string { return oldObj.AuthorizedCIDRs }
)

func validateCustomerAPIProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.CustomerAPIProfile) field.ErrorList {
	errs := field.ErrorList{}

	// Visibility      Visibility `json:"visibility,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("visibility"), &newObj.Visibility, nil)...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("visibility"), &newObj.Visibility, safe.Field(oldObj, toAPIVisibility))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("visibility"), &newObj.Visibility, nil, metadataapi.ValidVisibility, nil)...)

	// AuthorizedCIDRs []string   `json:"authorizedCidrs,omitempty"`
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("authorizedCidrs"), newObj.AuthorizedCIDRs, nil, 500)...)
	errs = append(errs, MinItems(ctx, op, fldPath.Child("authorizedCidrs"), newObj.AuthorizedCIDRs, nil, 1)...)
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
	toIngressType = func(oldObj *coreapi.CustomerIngressProfile) *metadataapi.IngressType { return &oldObj.Type }
)

func validateCustomerIngressProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.CustomerIngressProfile) field.ErrorList {
	errs := field.ErrorList{}

	// Type      IngressType `json:"type,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("type"), &newObj.Type, nil)...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toIngressType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, nil, metadataapi.ValidIngressTypes, nil)...)

	return errs
}

var (
	toAPIURL = func(oldObj *coreapi.ServiceProviderAPIProfile) *string { return &oldObj.URL }
)

func validateServiceProviderAPIProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ServiceProviderAPIProfile) field.ErrorList {
	errs := field.ErrorList{}

	// URL             string     `json:"url,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("url"), &newObj.URL, safe.Field(oldObj, toAPIURL))...)

	return errs
}

var (
	toPlatformManagedResourceGroup    = func(oldObj *coreapi.CustomerPlatformProfile) *string { return &oldObj.ManagedResourceGroup }
	toPlatformSubnetID                = func(oldObj *coreapi.CustomerPlatformProfile) *azcorearm.ResourceID { return oldObj.SubnetID }
	toPlatformVnetIntegrationSubnetID = func(oldObj *coreapi.CustomerPlatformProfile) *azcorearm.ResourceID {
		return oldObj.VnetIntegrationSubnetID
	}
	toPlatformOutboundType           = func(oldObj *coreapi.CustomerPlatformProfile) *metadataapi.OutboundType { return &oldObj.OutboundType }
	toPlatformNetworkSecurityGroupID = func(oldObj *coreapi.CustomerPlatformProfile) *azcorearm.ResourceID {
		return oldObj.NetworkSecurityGroupID
	}
	toPlatformOperatorsAuthentication = func(oldObj *coreapi.CustomerPlatformProfile) *coreapi.OperatorsAuthenticationProfile {
		return &oldObj.OperatorsAuthentication
	}
)

// Platform                CustomerPlatformProfile             `json:"platform,omitempty"`
func validateCustomerPlatformProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.CustomerPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	//ManagedResourceGroup    string                         `json:"managedResourceGroup,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("managedResourceGroup"), &newObj.ManagedResourceGroup, nil)...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("managedResourceGroup"), &newObj.ManagedResourceGroup, safe.Field(oldObj, toPlatformManagedResourceGroup))...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("managedResourceGroup"), &newObj.ManagedResourceGroup, nil, resourceGroupNameRegex, resourceGroupNameErrorString)...)

	//SubnetID                string                         `json:"subnetId,omitempty"`
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("subnetId"), newObj.SubnetID, safe.Field(oldObj, toPlatformSubnetID))...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("subnetId"), newObj.SubnetID, safe.Field(oldObj, toPlatformSubnetID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("subnetId"), newObj.SubnetID, safe.Field(oldObj, toPlatformSubnetID), "Microsoft.Network/virtualNetworks/subnets")...)
	// Note: DifferentResourceGroupNameFromResourceID for subnetId is performed at the cluster
	// peer-field level (it's a cross-field check against ManagedResourceGroup); duplicating it
	// here would emit the same error twice for the same input.

	// VnetIntegrationSubnetID *azcorearm.ResourceID `json:"vnetIntegrationSubnetId,omitempty"`
	// vnetIntegrationSubnetId was added in v20251223preview, so it's optional for backwards compatibility
	// TODO: When we remove the v20240610preview API we should remove the nil check here and add validate.RequiredValue
	// for vnetIntegrationSubnetId
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("vnetIntegrationSubnetId"), newObj.VnetIntegrationSubnetID, safe.Field(oldObj, toPlatformVnetIntegrationSubnetID))...)
	if newObj.VnetIntegrationSubnetID != nil {
		errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("vnetIntegrationSubnetId"), newObj.VnetIntegrationSubnetID, safe.Field(oldObj, toPlatformVnetIntegrationSubnetID), "Microsoft.Network/virtualNetworks/subnets")...)
		errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, fldPath.Child("vnetIntegrationSubnetId"), newObj.VnetIntegrationSubnetID, nil, newObj.ManagedResourceGroup)...)
		// SameSubscription is validated in validateResourceIDsAgainstClusterID against cluster subscription
		errs = append(errs, SameVirtualNetwork(ctx, op, fldPath.Child("vnetIntegrationSubnetId"), newObj.VnetIntegrationSubnetID, nil, newObj.SubnetID)...)
	}

	//OutboundType            OutboundType                   `json:"outboundType,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("outboundType"), &newObj.OutboundType, safe.Field(oldObj, toPlatformOutboundType))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("outboundType"), &newObj.OutboundType, safe.Field(oldObj, toPlatformOutboundType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("outboundType"), &newObj.OutboundType, nil, metadataapi.ValidOutboundTypes, nil)...)

	//NetworkSecurityGroupID  string                         `json:"networkSecurityGroupId,omitempty"`
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("networkSecurityGroupId"), newObj.NetworkSecurityGroupID, safe.Field(oldObj, toPlatformNetworkSecurityGroupID))...)
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("networkSecurityGroupId"), newObj.NetworkSecurityGroupID, safe.Field(oldObj, toPlatformNetworkSecurityGroupID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("networkSecurityGroupId"), newObj.NetworkSecurityGroupID, safe.Field(oldObj, toPlatformNetworkSecurityGroupID), "Microsoft.Network/networkSecurityGroups")...)
	// Note: SameSubscription and DifferentResourceGroupNameFromResourceID for
	// networkSecurityGroupId are performed at the cluster peer-field level in
	// validateResourceIDsAgainstClusterID.

	//OperatorsAuthentication OperatorsAuthenticationProfile `json:"operatorsAuthentication,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("operatorsAuthentication"), &newObj.OperatorsAuthentication, safe.Field(oldObj, toPlatformOperatorsAuthentication))...)
	errs = append(errs, validateOperatorsAuthenticationProfile(ctx, op, fldPath.Child("operatorsAuthentication"), &newObj.OperatorsAuthentication, safe.Field(oldObj, toPlatformOperatorsAuthentication))...)

	//ContainerRegistry       ContainerRegistryProfile             `json:"containerRegistry,omitzero"`
	errs = append(errs, validateContainerRegistryPullCredentials(ctx, op, fldPath.Child("containerRegistry", "managedIdentity"), newObj.ContainerRegistry.PullManagedIdentity, safe.Field(oldObj, toPlatformContainerRegistryPullMI), newObj.ManagedResourceGroup)...)

	return errs
}

func validateContainerRegistryPullCredentials(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj *azcorearm.ResourceID, oldObj *azcorearm.ResourceID, managedResourceGroup string) field.ErrorList {
	errs := field.ErrorList{}
	if newObj == nil {
		return errs
	}
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath, newObj, oldObj, "Microsoft.ManagedIdentity/userAssignedIdentities")...)
	errs = append(errs, DifferentResourceGroupNameFromResourceID(ctx, op, fldPath, newObj, oldObj, managedResourceGroup)...)
	return errs
}

var (
	toPlatformContainerRegistryPullMI = func(oldObj *coreapi.CustomerPlatformProfile) *azcorearm.ResourceID {
		return oldObj.ContainerRegistry.PullManagedIdentity
	}
)

var (
	toServiceProviderPlatformProfileIssuerURL = func(oldObj *coreapi.ServiceProviderPlatformProfile) *string { return &oldObj.IssuerURL }
)

// Platform                CustomerPlatformProfile             `json:"platform,omitempty"`
func validateServiceProviderPlatformProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ServiceProviderPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	//IssuerURL               string                         `json:"issuerUrl,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("issuerUrl"), &newObj.IssuerURL, safe.Field(oldObj, toServiceProviderPlatformProfileIssuerURL))...)

	return errs
}

var (
	toAuthenticationUserAssignedIdentities = func(oldObj *coreapi.OperatorsAuthenticationProfile) *coreapi.UserAssignedIdentitiesProfile {
		return &oldObj.UserAssignedIdentities
	}
)

func validateOperatorsAuthenticationProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.OperatorsAuthenticationProfile) field.ErrorList {
	errs := field.ErrorList{}

	//UserAssignedIdentities UserAssignedIdentitiesProfile `json:"userAssignedIdentities,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("userAssignedIdentities"), &newObj.UserAssignedIdentities, safe.Field(oldObj, toAuthenticationUserAssignedIdentities))...)
	errs = append(errs, validateUserAssignedIdentitiesProfile(ctx, op, fldPath.Child("userAssignedIdentities"), &newObj.UserAssignedIdentities, safe.Field(oldObj, toAuthenticationUserAssignedIdentities))...)

	return errs
}

var (
	toUserAssignedIdentitiesControlPlaneOperators = func(oldObj *coreapi.UserAssignedIdentitiesProfile) map[string]*azcorearm.ResourceID {
		return oldObj.ControlPlaneOperators
	}
	toUserAssignedIdentitiesDataPlaneOperators = func(oldObj *coreapi.UserAssignedIdentitiesProfile) map[string]*azcorearm.ResourceID {
		return oldObj.DataPlaneOperators
	}
	toUserAssignedIdentitiesServiceManagedIdentity = func(oldObj *coreapi.UserAssignedIdentitiesProfile) *azcorearm.ResourceID {
		return oldObj.ServiceManagedIdentity
	}
)

func validateUserAssignedIdentitiesProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.UserAssignedIdentitiesProfile) field.ErrorList {
	errs := field.ErrorList{}

	//ControlPlaneOperators  map[string]string `json:"controlPlaneOperators,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("controlPlaneOperators"), newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators))...)
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("controlPlaneOperators"),
		newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators),
		validate.RequiredValue,
	)...)
	// even though it's not listed, prior validation had the value required.
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("controlPlaneOperators"),
		newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators),
		nil,
		validate.RequiredPointer,
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("controlPlaneOperators"),
		newObj.ControlPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesControlPlaneOperators),
		nil,
		newRestrictedResourceID("Microsoft.ManagedIdentity/userAssignedIdentities"),
	)...)

	//DataPlaneOperators     map[string]string `json:"dataPlaneOperators,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("dataPlaneOperators"), newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators))...)
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("dataPlaneOperators"),
		newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators),
		validate.RequiredValue,
	)...)
	// even though it's not listed, prior validation had the value required.
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("dataPlaneOperators"),
		newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators),
		nil,
		validate.RequiredPointer,
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("dataPlaneOperators"),
		newObj.DataPlaneOperators, safe.Field(oldObj, toUserAssignedIdentitiesDataPlaneOperators),
		nil,
		newRestrictedResourceID("Microsoft.ManagedIdentity/userAssignedIdentities"),
	)...)

	//ServiceManagedIdentity string            `json:"serviceManagedIdentity,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("serviceManagedIdentity"), newObj.ServiceManagedIdentity, safe.Field(oldObj, toUserAssignedIdentitiesServiceManagedIdentity))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("serviceManagedIdentity"), newObj.ServiceManagedIdentity, safe.Field(oldObj, toUserAssignedIdentitiesServiceManagedIdentity), "Microsoft.ManagedIdentity/userAssignedIdentities")...)

	// Managed identity resource IDs must be unique across control-plane operators,
	// data-plane operators, and the service managed identity within a cluster
	errs = append(errs, validateManagedIdentitiesUniqueWithinCluster(fldPath, newObj)...)

	return errs
}

// validateManagedIdentitiesUniqueWithinCluster ensures that each managed identity
// resource ID used by control-plane operators, data-plane operators, or the
// service managed identity appears at most once within the cluster.
// This restriction may be relaxed in the future following investigation and decisions in ARO-21615.
func validateManagedIdentitiesUniqueWithinCluster(fldPath *field.Path, newObj *coreapi.UserAssignedIdentitiesProfile) field.ErrorList {
	observed := map[string]*field.Path{}
	var errs field.ErrorList

	record := func(identity *azcorearm.ResourceID, identityPath *field.Path) {
		if identity == nil {
			return
		}
		key := strings.ToLower(identity.String())
		if _, ok := observed[key]; ok {
			errs = append(errs, field.Invalid(
				identityPath,
				identity.String(),
				fmt.Sprintf("managed identity with resource id '%s' must be unique within the cluster", identity.String()),
			))
			return
		}
		observed[key] = identityPath
	}

	for _, operatorName := range slices.Sorted(maps.Keys(newObj.ControlPlaneOperators)) {
		record(newObj.ControlPlaneOperators[operatorName], fldPath.Child("controlPlaneOperators").Key(operatorName))
	}
	for _, operatorName := range slices.Sorted(maps.Keys(newObj.DataPlaneOperators)) {
		record(newObj.DataPlaneOperators[operatorName], fldPath.Child("dataPlaneOperators").Key(operatorName))
	}
	record(newObj.ServiceManagedIdentity, fldPath.Child("serviceManagedIdentity"))

	return errs
}

var (
	toClusterAutoscalingProfileMaxNodesTotal               = func(oldObj *coreapi.ClusterAutoscalingProfile) *int32 { return &oldObj.MaxNodesTotal }
	toClusterAutoscalingProfileMaxPodGracePeriodSeconds    = func(oldObj *coreapi.ClusterAutoscalingProfile) *int32 { return &oldObj.MaxPodGracePeriodSeconds }
	toClusterAutoscalingProfileMaxNodeProvisionTimeSeconds = func(oldObj *coreapi.ClusterAutoscalingProfile) *int32 { return &oldObj.MaxNodeProvisionTimeSeconds }
	toClusterAutoscalingProfilePodPriorityThreshold        = func(oldObj *coreapi.ClusterAutoscalingProfile) *int32 { return &oldObj.PodPriorityThreshold }
)

func validateClusterAutoscalingProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ClusterAutoscalingProfile) field.ErrorList {
	errs := field.ErrorList{}
	//MaxNodesTotal               int32 `json:"maxNodesTotal,omitempty"`
	// 0 is the minimum value for maxNodesTotal in the cluster autoscaler.
	// This aligns with the Cluster Service behavior where DefaultMaxNodesTotal = 0 indicates default behavior.
	// Note: This doesn't mean truly unlimited nodes - the cluster size limit of 500 nodes is still enforced
	// through nodepool validation during nodepool creation/update operations.
	// Negative values are rejected.
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("maxNodesTotal"), &newObj.MaxNodesTotal, safe.Field(oldObj, toClusterAutoscalingProfileMaxNodesTotal), 0)...)
	// 500 is currently the hardcoded limit of the maximum number of nodes allowed across all node pools in a cluster.
	// In the future this limit could be influenced by the topology of the management cluster
	// (provision shard) where the HCP is deployed: https://issues.redhat.com/browse/ARO-22019
	errs = append(errs, Maximum(ctx, op, fldPath.Child("maxNodesTotal"), &newObj.MaxNodesTotal, safe.Field(oldObj, toClusterAutoscalingProfileMaxNodesTotal), 500)...)

	//MaxPodGracePeriodSeconds    int32 `json:"maxPodGracePeriodSeconds,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maxPodGracePeriodSeconds"), &newObj.MaxPodGracePeriodSeconds, safe.Field(oldObj, toClusterAutoscalingProfileMaxPodGracePeriodSeconds))...)
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("maxPodGracePeriodSeconds"), &newObj.MaxPodGracePeriodSeconds, safe.Field(oldObj, toClusterAutoscalingProfileMaxPodGracePeriodSeconds), 1)...)

	//MaxNodeProvisionTimeSeconds int32 `json:"maxNodeProvisionTimeSeconds,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("maxNodeProvisionTimeSeconds"), &newObj.MaxNodeProvisionTimeSeconds, safe.Field(oldObj, toClusterAutoscalingProfileMaxNodeProvisionTimeSeconds))...)
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("maxNodeProvisionTimeSeconds"), &newObj.MaxNodeProvisionTimeSeconds, safe.Field(oldObj, toClusterAutoscalingProfileMaxNodeProvisionTimeSeconds), 1)...)

	//PodPriorityThreshold        int32 `json:"podPriorityThreshold,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("podPriorityThreshold"), &newObj.PodPriorityThreshold, safe.Field(oldObj, toClusterAutoscalingProfilePodPriorityThreshold))...)

	return errs
}

var (
	toEtcdProfileDataEncryption = func(oldObj *coreapi.EtcdProfile) *coreapi.EtcdDataEncryptionProfile { return &oldObj.DataEncryption }
)

func validateEtcdProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.EtcdProfile) field.ErrorList {
	errs := field.ErrorList{}

	//DataEncryption EtcdDataEncryptionProfile `json:"dataEncryption,omitempty"`
	errs = append(errs, validateEtcdDataEncryptionProfile(ctx, op, fldPath.Child("dataEncryption"), &newObj.DataEncryption, safe.Field(oldObj, toEtcdProfileDataEncryption))...)

	return errs
}

var (
	toEtcdDataEncryptionProfileKeyManagementMode = func(oldObj *coreapi.EtcdDataEncryptionProfile) *metadataapi.EtcdDataEncryptionKeyManagementModeType {
		return &oldObj.KeyManagementMode
	}
	toEtcdDataEncryptionProfileCustomerManaged = func(oldObj *coreapi.EtcdDataEncryptionProfile) *coreapi.CustomerManagedEncryptionProfile {
		return oldObj.CustomerManaged
	}
)

func validateEtcdDataEncryptionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.EtcdDataEncryptionProfile) field.ErrorList {
	errs := field.ErrorList{}

	//KeyManagementMode EtcdDataEncryptionKeyManagementModeType `json:"keyManagementMode,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("keyManagementMode"), &newObj.KeyManagementMode, safe.Field(oldObj, toEtcdDataEncryptionProfileKeyManagementMode))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("keyManagementMode"), &newObj.KeyManagementMode, safe.Field(oldObj, toEtcdDataEncryptionProfileKeyManagementMode))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("keyManagementMode"), &newObj.KeyManagementMode, safe.Field(oldObj, toEtcdDataEncryptionProfileKeyManagementMode), metadataapi.ValidEtcdDataEncryptionKeyManagementModeType, nil)...)

	//CustomerManaged   *CustomerManagedEncryptionProfile       `json:"customerManaged,omitempty"`
	union := validate.NewDiscriminatedUnionMembership("keyManagementMode", validate.NewDiscriminatedUnionMember("customerManaged", "CustomerManaged"))
	discriminatorExtractor := func(obj *coreapi.EtcdDataEncryptionProfile) metadataapi.EtcdDataEncryptionKeyManagementModeType {
		return obj.KeyManagementMode
	}
	isCustomerManagedSetFn := func(obj *coreapi.EtcdDataEncryptionProfile) bool {
		return obj.CustomerManaged != nil
	}
	// this verifies that CustomerManaged is set iff keyManagementMode==CustomerManaged
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isCustomerManagedSetFn)...)
	errs = append(errs, validateCustomerManagedEncryptionProfile(ctx, op, fldPath.Child("customerManaged"), newObj.CustomerManaged, safe.Field(oldObj, toEtcdDataEncryptionProfileCustomerManaged))...)

	return errs
}

var (
	toCustomerManagedEncryptionProfileEncryptionType = func(oldObj *coreapi.CustomerManagedEncryptionProfile) *metadataapi.CustomerManagedEncryptionType {
		return &oldObj.EncryptionType
	}
	toEtcdDataEncryptionProfileKms = func(oldObj *coreapi.CustomerManagedEncryptionProfile) *coreapi.KmsEncryptionProfile {
		return oldObj.Kms
	}
)

func validateCustomerManagedEncryptionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.CustomerManagedEncryptionProfile) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//EncryptionType CustomerManagedEncryptionType `json:"encryptionType,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("encryptionType"), &newObj.EncryptionType, safe.Field(oldObj, toCustomerManagedEncryptionProfileEncryptionType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("encryptionType"), &newObj.EncryptionType, safe.Field(oldObj, toCustomerManagedEncryptionProfileEncryptionType), metadataapi.ValidCustomerManagedEncryptionType, nil)...)

	//Kms            *KmsEncryptionProfile         `json:"kms,omitempty"`
	union := validate.NewDiscriminatedUnionMembership("encryptionType", validate.NewDiscriminatedUnionMember("kms", "KMS"))
	discriminatorExtractor := func(obj *coreapi.CustomerManagedEncryptionProfile) metadataapi.CustomerManagedEncryptionType {
		return obj.EncryptionType
	}
	isCustomerManagedSetFn := func(obj *coreapi.CustomerManagedEncryptionProfile) bool {
		return obj.Kms != nil
	}
	// this verifies that Kms is set iff encryptionType==KMS
	errs = append(errs, validate.DiscriminatedUnion(ctx, op, fldPath, newObj, oldObj,
		union, discriminatorExtractor, isCustomerManagedSetFn)...)
	errs = append(errs, validateKmsEncryptionProfile(ctx, op, fldPath.Child("kms"), newObj.Kms, safe.Field(oldObj, toEtcdDataEncryptionProfileKms))...)

	return errs
}

var (
	toKmsEncryptionProfileVisibility = func(oldObj *coreapi.KmsEncryptionProfile) *metadataapi.KeyVaultVisibility { return &oldObj.Visibility }
	toKmsEncryptionProfileActiveKey  = func(oldObj *coreapi.KmsEncryptionProfile) *coreapi.KmsKey { return &oldObj.ActiveKey }
)

func validateKmsEncryptionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.KmsEncryptionProfile) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	// Visibility KeyVaultVisibility `json:"visibility,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("visibility"), &newObj.Visibility, safe.Field(oldObj, toKmsEncryptionProfileVisibility))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("visibility"), &newObj.Visibility, safe.Field(oldObj, toKmsEncryptionProfileVisibility))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("visibility"), &newObj.Visibility, safe.Field(oldObj, toKmsEncryptionProfileVisibility), metadataapi.ValidKeyVaultVisibility, nil)...)

	//ActiveKey KmsKey `json:"activeKey,omitempty"`
	errs = append(errs, validateKmsKey(ctx, op, fldPath.Child("activeKey"), &newObj.ActiveKey, safe.Field(oldObj, toKmsEncryptionProfileActiveKey))...)

	return errs
}

var (
	toKmsKeyName      = func(oldObj *coreapi.KmsKey) *string { return &oldObj.Name }
	toKmsKeyVaultName = func(oldObj *coreapi.KmsKey) *string { return &oldObj.VaultName }
	toKmsKeyVersion   = func(oldObj *coreapi.KmsKey) *string { return &oldObj.Version }
)

func validateKmsKey(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.KmsKey) field.ErrorList {
	errs := field.ErrorList{}

	//Name      string `json:"name"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toKmsKeyName))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("name"), &newObj.Name, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("name"), &newObj.Name, nil, 255)...)

	//VaultName string `json:"vaultName"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("vaultName"), &newObj.VaultName, safe.Field(oldObj, toKmsKeyVaultName))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("vaultName"), &newObj.VaultName, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("vaultName"), &newObj.VaultName, nil, 255)...)

	//Version   string `json:"version"`
	// The version field was made mutable in version 2026-06-30-preview.
	apiVersion := metadataapi.APIVersionFromOptions(op.Options)
	if len(apiVersion) > 0 && apiVersion.LT(metadataapi.APIVersionV20260630Preview) {
		errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, toKmsKeyVersion))...)
	}
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("version"), &newObj.Version, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("version"), &newObj.Version, nil, 255)...)

	return errs
}

var (
	toPlatformClusterImageRegistryState = func(oldObj *coreapi.ClusterImageRegistryProfile) *metadataapi.ClusterImageRegistryState {
		return &oldObj.State
	}
)

func validateClusterImageRegistryProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ClusterImageRegistryProfile) field.ErrorList {
	errs := field.ErrorList{}

	//State ClusterImageRegistryState `json:"state,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("state"), &newObj.State, safe.Field(oldObj, toPlatformClusterImageRegistryState))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("state"), &newObj.State, safe.Field(oldObj, toPlatformClusterImageRegistryState))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("state"), &newObj.State, safe.Field(oldObj, toPlatformClusterImageRegistryState), metadataapi.ValidClusterImageRegistryStates, nil)...)

	return errs
}

var (
	toImageDigestMirrorSource  = func(oldObj *coreapi.ImageDigestMirror) *string { return &oldObj.Source }
	toImageDigestMirrorMirrors = func(oldObj *coreapi.ImageDigestMirror) []string { return oldObj.Mirrors }
)

func validateImageDigestMirror(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ImageDigestMirror) field.ErrorList {
	errs := field.ErrorList{}

	//Source string `json:"source,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("source"), &newObj.Source, safe.Field(oldObj, toImageDigestMirrorSource))...)
	errs = append(errs, ImageRegistry(ctx, op, fldPath.Child("source"), &newObj.Source, safe.Field(oldObj, toImageDigestMirrorSource))...)

	// This is OpenShift's regex for the source image registry. Their pattern
	// is more permissive than what we allow (e.g. casing of letters) but acts
	// as a final check to make sure OpenShift will accept it.
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("source"), &newObj.Source, safe.Field(oldObj, toImageDigestMirrorSource), imageDigestSourceRegistryRegex, imageDigestSourceRegistryErrorString)...)

	//Mirrors []string `json:"mirrors,omitempty"`
	errs = append(errs, MinItems(ctx, op, fldPath.Child("mirrors"), newObj.Mirrors, safe.Field(oldObj, toImageDigestMirrorMirrors), 1)...)
	errs = append(errs, MaxItems(ctx, op, fldPath.Child("mirrors"), newObj.Mirrors, safe.Field(oldObj, toImageDigestMirrorMirrors), 255)...)
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("mirrors"),
		newObj.Mirrors, safe.Field(oldObj, toImageDigestMirrorMirrors),
		nil, nil,
		ImageRegistry,
	)...)

	// This is OpenShift's regex for a mirrored image registry. Their pattern
	// is more permissive than what we allow (e.g. casing of letters) but acts
	// as a final check to make sure OpenShift will accept it.
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("mirrors"),
		newObj.Mirrors, safe.Field(oldObj, toImageDigestMirrorMirrors),
		nil, nil,
		func(ctx context.Context, op operation.Operation, fldPath *field.Path, newValue, oldValue *string) field.ErrorList {
			return MatchesRegex(ctx, op, fldPath, newValue, oldValue, imageDigestMirroredRegistryRegex, imageDigestMirroredRegistryErrorString)
		},
	)...)

	//MirrorSourcePolicy MirrorSourcePolicy `json:"mirrorSourcePolicy,omitempty"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("mirrorSourcePolicy"), &newObj.MirrorSourcePolicy, nil, metadataapi.ValidMirrorSourcePolicies, nil)...)

	return errs
}

var (
	toManagedServiceIdentityPrincipalID            = func(oldObj *coreapi.ManagedServiceIdentity) *string { return &oldObj.PrincipalID }
	toManagedServiceIdentityTenantID               = func(oldObj *coreapi.ManagedServiceIdentity) *string { return &oldObj.TenantID }
	toManagedServiceIdentityType                   = func(oldObj *coreapi.ManagedServiceIdentity) *coreapi.ManagedServiceIdentityType { return &oldObj.Type }
	toManagedServiceIdentityUserAssignedIdentities = func(oldObj *coreapi.ManagedServiceIdentity) map[string]*coreapi.UserAssignedIdentity {
		return oldObj.UserAssignedIdentities
	}
)

func validateManagedServiceIdentity(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.ManagedServiceIdentity) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//PrincipalID            string                           `json:"principalId,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("principalId"), &newObj.PrincipalID, safe.Field(oldObj, toManagedServiceIdentityPrincipalID))...)
	//TenantID               string                           `json:"tenantId,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("tenantId"), &newObj.TenantID, safe.Field(oldObj, toManagedServiceIdentityTenantID))...)

	//Type                   ManagedServiceIdentityType       `json:"type"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("type"), &newObj.Type, nil)...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("state"), &newObj.Type, safe.Field(oldObj, toManagedServiceIdentityType), coreapi.ValidManagedServiceIdentityTypes, nil)...)

	//UserAssignedIdentities map[string]*UserAssignedIdentity `json:"userAssignedIdentities,omitempty"`
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("userAssignedIdentities"),
		newObj.UserAssignedIdentities, safe.Field(oldObj, toManagedServiceIdentityUserAssignedIdentities),
		validate.RequiredValue,
	)...)
	errs = append(errs, EachMapKey(ctx, op, fldPath.Child("userAssignedIdentities"),
		newObj.UserAssignedIdentities, safe.Field(oldObj, toManagedServiceIdentityUserAssignedIdentities),
		newRestrictedResourceIDString("Microsoft.ManagedIdentity/userAssignedIdentities"),
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("userAssignedIdentities"),
		newObj.UserAssignedIdentities, safe.Field(oldObj, toManagedServiceIdentityUserAssignedIdentities),
		nil,
		validateUserAssignedIdentity,
	)...)

	return errs
}

var (
	toUserAssignedIdentityClientID = func(oldObj **coreapi.UserAssignedIdentity) *string {
		if oldObj == nil || *oldObj == nil {
			return nil
		}
		return (*oldObj).ClientID
	}
	toUserAssignedIdentityPrincipalID = func(oldObj **coreapi.UserAssignedIdentity) *string {
		if oldObj == nil || *oldObj == nil {
			return nil
		}
		return (*oldObj).PrincipalID
	}
)

func validateUserAssignedIdentity(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj **coreapi.UserAssignedIdentity) field.ErrorList {
	if newObj == nil || *newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//ClientID    *string `json:"clientId,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("clientId"), (*newObj).ClientID, safe.Field(oldObj, toUserAssignedIdentityClientID))...)

	//PrincipalID *string `json:"principalId,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("principalId"), (*newObj).PrincipalID, safe.Field(oldObj, toUserAssignedIdentityPrincipalID))...)

	return errs
}
