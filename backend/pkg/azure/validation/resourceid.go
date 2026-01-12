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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

var (
	subnetResourceType                         azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Network/virtualNetworks/subnets"))
	vnetResourceType                           azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Network/virtualNetworks"))
	nsgResourceType                            azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Network/networkSecurityGroups"))
	publicDNSZoneResourceType                  azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Network/dnsZones"))
	userAssignedManagedIdentityType            azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.ManagedIdentity/userAssignedIdentities"))
	acrResourceType                            azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.ContainerRegistry/registries"))
	roleDefinitionResourceType                 azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Authorization/roleDefinitions"))
	resourceGroupResourceType                  azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Resources/resourceGroups"))
	diskEncryptionSetResourceType              azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.Compute/diskEncryptionSets"))
	keyVaultResourceType                       azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.KeyVault/vaults"))
	containerServiceManagedClusterResourceType azcorearm.ResourceType = api.Must(azcorearm.ParseResourceType("Microsoft.ContainerService/managedClusters"))
)

// ValidateResourceGroupScopedResourceID validates that the Azure Resource ID
// `resourceID“ is a valid resource group scoped resource id of the Azure Resource
// Type `resourceType` an Azure Resource ID. It also validates that the
// resource ID has a name.
// The validations that are performed are:
// - The resource type of the resourceID matches the provided resourceType
// - The Azure Subscription ID of resourceID can be parsed and it is not empty
// - The Azure Resource Group Name of resourceID can be parsed and it is not empty
// - The Azure Resource Name of resourceID can be parsed and it is not empty
func ValidateResourceGroupScopedResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path,
	resourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType,
) field.ErrorList {
	errs := field.ErrorList{}

	if resourceID == nil {
		return nil
	}

	resourceResourceType := resourceID.ResourceType
	if !strings.EqualFold(resourceResourceType.String(), resourceType.String()) {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), fmt.Sprintf("'%s' is not a valid '%s' Resource ID", resourceID.String(), resourceType)))
	}

	if len(resourceID.SubscriptionID) == 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "subscription id could not be parsed"))
	}

	if len(resourceID.ResourceGroupName) == 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "resource group could not be parsed"))
	}

	if len(resourceID.Name) == 0 {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), "resource name could not be parsed"))
	}

	return errs
}

// ValidateSubnetResourceID validates that the Azure Subnet Resource ID
// specified in `resourceID` is a valid Azure Subnet Resource ID.
// The validations that are performed are:
//   - The resource type of the subnet resource id is the expected one for Azure
//     Subnets.
func ValidateSubnetResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, subnetResourceType)
}

// ValidateVnetResourceID validates that the Azure VNet Resource ID
// specified in `resourceID` is a valid Azure VNet Resource ID.
// The validations that are performed are:
//   - The resource type of the vnet resource id is the expected one for Azure
//     VNets.
func ValidateVnetResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, vnetResourceType)
}

// ValidateNetworkSecurityGroupResourceID validates that the Azure Network Security Group
// Resource ID string specified in `resourceID` is a valid Azure Network
// Security Group Resource ID.
// The validations that are performed are:
//   - The resource type of the network security group resource id is the
//     expected one for Azure Network Security Groups
func ValidateNetworkSecurityGroupResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, nsgResourceType)
}

// ValidatePublicDNSZoneResourceID validates that the Azure Public DNS Zone
// Resource ID string specified in `resourceID` is a valid Azure Public DNS Zone
// Resource ID.
// The validations that are performed are:
//   - The resource type of the  Public DNS Zone resource id is the
//     expected one for Azure Network Security Groups
func ValidatePublicDNSZoneResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, publicDNSZoneResourceType)
}

// ValidateUserAssignedManagedIdentity validates that the Azure User-Assigned Managed
// `resourceID` is a valid Azure User-Assigned Managed Identity Resource ID.
// The validations that are performed are:
//   - The resource type of the User-Assigned Managed Identity resource id is the
//     expected one for Azure User-Assigned Managed Identities
func ValidateUserAssignedManagedIdentity(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, userAssignedManagedIdentityType)
}

// ValidateACRResourceID  validates that the Azure Container Registry
// `resourceID` is a valid Azure Container Registry Resource ID.
// The validations that are performed are:
//   - The resource type of the Azure Container Registry resource id is the
//     expected one for Azure Container Registries
func ValidateACRResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, acrResourceType)
}

// ValidateRoleDefinitionResourceID validates that the Azure Role Definition
// `resourceID` is a valid Azure Role Definition Resource ID.
// The validations that are performed are:
//   - The resource type of the role definition resource id is the expected one
//     for Azure Role Definitions
func ValidateRoleDefinitionResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	var errs field.ErrorList

	if resourceID == nil {
		return nil
	}

	if !strings.EqualFold(resourceID.ResourceType.String(), roleDefinitionResourceType.String()) {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), fmt.Sprintf("'%s' is not a valid '%s' Resource ID", resourceID.String(), roleDefinitionResourceType)))
	}

	return errs
}

// ValidateResourceGroupResourceID validates that the Azure Resource Group Resource
// `resourceID` is a valid Azure Resource Group Resource ID.
// The validations that are performed are:
//   - The resource type of the resource group resource id is the expected one
//     for Azure Resource Groups.
func ValidateResourceGroupResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	var errs field.ErrorList

	if resourceID == nil {
		return nil
	}

	if !strings.EqualFold(resourceID.ResourceType.String(), resourceGroupResourceType.String()) {
		errs = append(errs, field.Invalid(fldPath, resourceID.String(), fmt.Sprintf("'%s' is not a valid '%s' Resource ID", resourceID.String(), resourceGroupResourceType)))
	}

	return errs
}

// ValidateDiskEncryptionSetResourceID validates that the Azure Disk Encryption Set
// `resourceID` is a valid Azure Disk Encryption Set Resource ID.
// The validations that are performed are:
//   - The resource type of the disk encryption set resource id is the expected one
//     for Azure Disk Encryption Sets
func ValidateDiskEncryptionSetResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, diskEncryptionSetResourceType)
}

// ValidateKeyVaultResourceID validates that the Azure Key Vault Resource
// `resourceID` is a valid Azure Key Vault Resource ID.
// The validations that are performed are:
//   - The resource type of the key vault resource id is the expected one
//     for Azure Key Vaults.
func ValidateKeyVaultResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, keyVaultResourceType)
}

// ValidateContainerServiceManagedClusterResourceID validates that the Azure Container Service Managed Cluster,
// (also known as AKS Cluster) `resourceID` is a valid Azure Container Service Managed Cluster Resource ID.
// The validations that are performed are:
//   - The resource type of the container service managed cluster resource id is the
//     expected one for Azure Container Service Managed Clusters.
func ValidateContainerServiceManagedClusterResourceID(ctx context.Context, op operation.Operation, fldPath *field.Path, resourceID *azcorearm.ResourceID) field.ErrorList {
	return ValidateResourceGroupScopedResourceID(ctx, op, fldPath, resourceID, containerServiceManagedClusterResourceType)
}
