package resourceid

import (
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ValidateResourceGroupScopedResourceID parses an Azure Resource ID
// specified in `resourceID` of Azure Resource Type `resourceType`.
// The Resource ID is assumed to be a Resource Group scoped Azure Resource ID.
// An error is returned if `resourceID` cannot be parsed.
// The validations that are performed are:
// - The resourceID is a valid Azure Resource ID
// - The resource type of the resourceID matches the provided resourceType
// - The Azure Subscription ID of resourceID can be parsed and it is not empty
// - The Azure Resource Group Name of resourceID can be parsed and it is not empty
// - The Azure Resource Name of resourceID can be parsed and it is not empty
func ValidateResourceGroupScopedResourceID(
	resourceID azcorearm.ResourceID, resourceType azcorearm.ResourceType) error {

	res, err := azcorearm.ParseResourceID(resourceID.String())
	if err != nil {
		return fmt.Errorf("'%v' is not a valid Azure Resource ID: %w", resourceID, err)
	}

	resourceResourceType := res.ResourceType
	if !strings.EqualFold(resourceResourceType.String(), resourceType.String()) {
		return fmt.Errorf("'%v' is not a valid '%s' Resource ID", resourceID, resourceType)
	}

	resourceSubscriptionId := res.SubscriptionID
	if resourceSubscriptionId == "" {
		return fmt.Errorf("error parsing '%v': subscription id could not be parsed", resourceID)
	}

	resourceResourceGroupName := res.ResourceGroupName
	if resourceResourceGroupName == "" {
		return fmt.Errorf("error parsing '%v': resource group could not be parsed", resourceID)
	}

	if res.Name == "" {
		return fmt.Errorf("error parsing '%s': '%v' resource name could not be parsed", resourceType, resourceID)
	}

	return nil
}

// ValidateSubnetResourceID parses an Azure Subnet Resource ID
// specified in `resourceID`. An error is returned if
// `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The subnet resource id is a valid Azure Resource ID
//   - The resource type of the subnet resource id is the expected one for Azure
//     Subnets.
func ValidateSubnetResourceID(resourceID azcorearm.ResourceID) error {
	subnetResourceTypeStr := "Microsoft.Network/virtualNetworks/subnets"
	subnetResourceType, err := azcorearm.ParseResourceType(subnetResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", subnetResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, subnetResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateVnetResourceID parses an Azure VNet Resource ID string
// specified in `resourceID`. An error is returned if `resourceID`
// cannot be parsed.
// The validations that are performed are:
//   - The vnet resource id is a valid Azure Resource ID
//   - The resource type of the vnet resource id is the expected one for Azure
//     VNets.
func ValidateVnetResourceID(resourceID azcorearm.ResourceID) error {
	vnetResourceTypeStr := "Microsoft.Network/virtualNetworks"
	vnetResourceType, err := azcorearm.ParseResourceType(vnetResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", vnetResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, vnetResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateNetworkSecurityGroupResourceID parses an Azure Network Security Group
// Resource ID string specified in `resourceID`. An error is returned
// if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The network security group resource id is a valid Azure Resource ID
//   - The resource type of the network security group resource id is the
//     expected one for Azure Network Security Groups
func ValidateNetworkSecurityGroupResourceID(resourceID azcorearm.ResourceID) error {
	nsgResourceTypeStr := "Microsoft.Network/networkSecurityGroups"
	nsgResourceType, err := azcorearm.ParseResourceType(nsgResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", nsgResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, nsgResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidatePublicDNSZoneResourceID parses an Azure Public DNS Zone
// Resource ID string specified in `resourceID`. An error is returned
// if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The Public DNS Zone resource id is a valid Azure Resource ID
//   - The resource type of the  Public DNS Zone resource id is the
//     expected one for Azure Network Security Groups
func ValidatePublicDNSZoneResourceID(resourceID azcorearm.ResourceID) error {
	publicDNSZoneResourceTypeStr := "Microsoft.Network/dnsZones"
	publicDNSZoneResourceType, err := azcorearm.ParseResourceType(publicDNSZoneResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", publicDNSZoneResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, publicDNSZoneResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateUserAssignedManagedIdentity parses an Azure User-Assigned Managed
// Identity Resource string specified in `resourceID`.
// An error is returned if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The User-Assigned Managed Identity resource id is a valid Azure Resource ID
//   - The resource type of the  User-Assigned Managed Identity resource id is the
//     expected one for Azure User-Assigned Managed Identities
func ValidateUserAssignedManagedIdentity(resourceID azcorearm.ResourceID) error {
	userAssignedManagedIdentityTypeStr := "Microsoft.ManagedIdentity/userAssignedIdentities"
	userAssignedManagedIdentityType, err := azcorearm.ParseResourceType(userAssignedManagedIdentityTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", userAssignedManagedIdentityTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, userAssignedManagedIdentityType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateACRResourceID parses an Azure Container Registry
// Resource string specified in `resourceID`.
// An error is returned if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The Azure Container Registry resource id is a valid Azure Resource ID
//   - The resource type of the Azure Container Registry resource id is the
//     expected one for Azure Container Registries
func ValidateACRResourceID(resourceID azcorearm.ResourceID) error {
	acrResourceTypeStr := "Microsoft.ContainerRegistry/registries"
	acrResourceType, err := azcorearm.ParseResourceType(acrResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", acrResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, acrResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateRoleDefinitionResourceID parses an Azure Role Definition
// Resource string specified in `resourceID`.
// An error is returned if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The role definition resource id is a valid Azure Resource ID
//   - The resource type of the role definition resource id is the expected one
//     for Azure Role Definitions
func ValidateRoleDefinitionResourceID(resourceID azcorearm.ResourceID) error {
	roleDefinitionResourceTypeStr := "Microsoft.Authorization/roleDefinitions"
	roleDefinitionResourceType, err := azcorearm.ParseResourceType(roleDefinitionResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", roleDefinitionResourceTypeStr, err)
	}

	res, err := azcorearm.ParseResourceID(resourceID.String())
	if err != nil {
		return fmt.Errorf("'%v' is not a valid Azure Resource ID: %w", resourceID, err)
	}

	resourceType := res.ResourceType
	if !strings.EqualFold(resourceType.String(), roleDefinitionResourceType.String()) {
		return fmt.Errorf("'%v' is not a valid '%s' Resource ID", resourceID, roleDefinitionResourceType)
	}

	return nil
}

// ValidateResourceGroupResourceID parses an Azure Resource Group Resource
// string specified in `resourceID`. An error is returned
// if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The resource group resource id is a valid Azure Resource ID
//   - The resource type of the resource group resource id is the expected one
//     for Azure Resource Groups.
func ValidateResourceGroupResourceID(resourceID azcorearm.ResourceID) error {
	resourceGroupResourceTypeStr := "Microsoft.Resources/resourceGroups"
	resourceGroupResourceType, err := azcorearm.ParseResourceType(resourceGroupResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", resourceGroupResourceTypeStr, err)
	}

	res, err := azcorearm.ParseResourceID(resourceID.String())
	if err != nil {
		return fmt.Errorf("'%v' is not a valid Azure Resource ID: %w", resourceID, err)
	}

	resourceType := res.ResourceType
	if !strings.EqualFold(resourceType.String(), resourceGroupResourceType.String()) {
		return fmt.Errorf("'%v' is not a valid '%s' Resource ID", resourceID, resourceGroupResourceType)
	}

	return nil
}

// ValidateDiskEncryptionSetResourceID parses an Azure Disk Encryption Set
// Resource string specified in `resourceID`.
// An error is returned if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The disk encryption set resource id is a valid Azure Resource ID
//   - The resource type of the disk encryption set resource id is the expected one
//     for Azure Disk Encryption Sets
func ValidateDiskEncryptionSetResourceID(resourceID azcorearm.ResourceID) error {
	diskEncryptionSetResourceTypeStr := "Microsoft.Compute/diskEncryptionSets"
	diskEncryptionSetResourceType, err := azcorearm.ParseResourceType(diskEncryptionSetResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", diskEncryptionSetResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, diskEncryptionSetResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateKeyVaultResourceID parses an Azure Key Vault Resource
// string specified in `resourceID`. An error is returned
// if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The key vault resource id is a valid Azure Resource ID
//   - The resource type of the key vault resource id is the expected one
//     for Azure Key Vaults.
func ValidateKeyVaultResourceID(resourceID azcorearm.ResourceID) error {
	keyVaultResourceTypeStr := "Microsoft.KeyVault/vaults"
	keyVaultResourceType, err := azcorearm.ParseResourceType(keyVaultResourceTypeStr)
	if err != nil {
		return fmt.Errorf("error parsing resource type '%s': %w", keyVaultResourceTypeStr, err)
	}

	err = ValidateResourceGroupScopedResourceID(resourceID, keyVaultResourceType)
	if err != nil {
		return err
	}

	return nil
}

// ValidateContainerServiceManagedClusterResourceID parses an Azure Container Service Managed Cluster,
// (also known as AKS Cluster) string specified in `resourceID`.
// An error is returned if `resourceID` cannot be parsed.
// The validations that are performed are:
//   - The container service managed cluster resource id is a valid Azure Resource ID
//   - The resource type of the container service managed cluster resource id is the
//     expected one for Azure Container Service Managed Clusters.
func ValidateContainerServiceManagedClusterResourceID(
	resourceID azcorearm.ResourceID) error {
	containerServiceManagedClusterResourceTypeStr := "Microsoft.ContainerService/managedClusters"
	containerServiceManagedClusterResourceType, err := azcorearm.ParseResourceType(
		containerServiceManagedClusterResourceTypeStr,
	)
	if err != nil {
		return fmt.Errorf(
			"error parsing resource type '%s': %w", containerServiceManagedClusterResourceTypeStr, err,
		)
	}

	err = ValidateResourceGroupScopedResourceID(
		resourceID, containerServiceManagedClusterResourceType,
	)
	if err != nil {
		return err
	}

	return nil
}

// ParseChildSubnetResourceID parses an Azure Subnet Resource Name `subnetName`
// string together with an Azure VNet Resource ID `vnetResourceID`. The
// returned ResourceID represents an Azure Subnet whose name is `subnetName` and
// that belongs to the Azure Subnet represented by `vnetResourceID`. An error
// is returned if they cannot be parsed.
// The validations that are performed are:
//   - The vnet resource id is a valid Azure Resource ID
//   - The resource type of the vnet resource id is the expected one for Azure
//     VNets.
func ParseChildSubnetResourceID(subnetName string, vnetResourceID azcorearm.ResourceID) (azcorearm.ResourceID, error) {
	err := ValidateVnetResourceID(vnetResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	subnetResourceID, err := azcorearm.ParseResourceID(fmt.Sprintf("%s/subnets/%s", vnetResourceID.String(), subnetName))
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	err = ValidateSubnetResourceID(*subnetResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return *subnetResourceID, nil
}

// VnetResourceIDFromSubnetResourceID attempts to parse an Azure Subnet Resource ID
// from a Azure VNet Resource ID string specified in `resourceID`. An error
// is returned if it cannot be parsed.
// The validations that are performed are:
// - The subnet resource id is a valid Azure Resource ID
//   - The resource type of the subnet resource id is the expected one for Azure
//     VNets.
func ParseVnetResourceIDFromSubnetResourceID(resourceID azcorearm.ResourceID) (azcorearm.ResourceID, error) {
	err := ValidateSubnetResourceID(resourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	if resourceID.Parent == nil {
		return azcorearm.ResourceID{}, fmt.Errorf("subnet resource id has no parent resource id")
	}

	vnetResourceID := *resourceID.Parent
	err = ValidateVnetResourceID(vnetResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return vnetResourceID, nil
}
