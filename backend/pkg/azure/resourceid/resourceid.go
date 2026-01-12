package resourceid

import (
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ParseResourceGroupScopedResourceID parses an Azure Resource ID string
// specified in `rawResourceID` of Azure Resource Type `resourceType`.
// The Resource ID is assumed to be a Resource Group scoped Azure Resource ID.
// An error is returned if `rawResourceID` cannot be parsed.
// The validations that are performed are:
// - The rawResourceID is a valid Azure Resource ID
// - The resource type of the rawResourceID matches the provided resourceType
// - The Azure Subscription ID of rawResourceID can be parsed and it is not empty
// - The Azure Resource Group Name of rawResourceID can be parsed and it is not empty
// - The Azure Resource Name of rawResourceID can be parsed and it is not empty
func ParseResourceGroupScopedResourceID(
	rawResourceID string, resourceType azcorearm.ResourceType) (azcorearm.ResourceID, error) {

	res, err := azcorearm.ParseResourceID(rawResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("'%s' is not a valid Azure Resource ID: %w", rawResourceID, err)
	}

	resourceResourceType := res.ResourceType
	if !strings.EqualFold(resourceResourceType.String(), resourceType.String()) {
		return azcorearm.ResourceID{},
			fmt.Errorf("'%s' is not a valid '%s' Resource ID", rawResourceID, resourceType)
	}

	resourceSubscriptionId := res.SubscriptionID
	if resourceSubscriptionId == "" {
		return azcorearm.ResourceID{},
			fmt.Errorf("error parsing '%s': subscription id could not be parsed", rawResourceID)
	}

	resourceResourceGroupName := res.ResourceGroupName
	if resourceResourceGroupName == "" {
		return azcorearm.ResourceID{},
			fmt.Errorf("error parsing '%s': resource group could not be parsed", rawResourceID)
	}

	if res.Name == "" {
		return azcorearm.ResourceID{}, fmt.Errorf(
			"error parsing '%s': '%s' resource name could not be parsed", resourceType, rawResourceID,
		)
	}

	return *res, nil
}

// ParseSubnetResourceID parses an Azure Subnet Resource ID string
// specified in `rawSubnetResourceID`. An error is returned if
// `rawSubnetResourceID` cannot be parsed.
// The validations that are performed are:
//   - The subnet resource id is a valid Azure Resource ID
//   - The resource type of the subnet resource id is the expected one for Azure
//     Subnets.
func ParseSubnetResourceID(rawSubnetResourceID string) (azcorearm.ResourceID, error) {
	subnetResourceTypeStr := "Microsoft.Network/virtualNetworks/subnets"
	subnetResourceType, err := azcorearm.ParseResourceType(subnetResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", subnetResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawSubnetResourceID, subnetResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseVnetResourceID parses an Azure VNet Resource ID string
// specified in `rawVnetResourceID`. An error is returned if `rawVnetResourceID`
// cannot be parsed.
// The validations that are performed are:
//   - The vnet resource id is a valid Azure Resource ID
//   - The resource type of the vnet resource id is the expected one for Azure
//     VNets.
func ParseVnetResourceID(rawVnetResourceID string) (azcorearm.ResourceID, error) {
	vnetResourceTypeStr := "Microsoft.Network/virtualNetworks"
	vnetResourceType, err := azcorearm.ParseResourceType(vnetResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", vnetResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawVnetResourceID, vnetResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseNetworkSecurityGroupResourceID parses an Azure Network Security Group
// Resource ID string specified in `rawNsgResourceID`. An error is returned
// if `rawNsgResourceID` cannot be parsed.
// The validations that are performed are:
//   - The network security group resource id is a valid Azure Resource ID
//   - The resource type of the network security group resource id is the
//     expected one for Azure Network Security Groups
func ParseNetworkSecurityGroupResourceID(rawNsgResourceID string) (azcorearm.ResourceID, error) {
	nsgResourceTypeStr := "Microsoft.Network/networkSecurityGroups"
	nsgResourceType, err := azcorearm.ParseResourceType(nsgResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", nsgResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawNsgResourceID, nsgResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParsePublicDNSZoneResourceID parses an Azure Public DNS Zone
// Resource ID string specified in `rawNsgResourceID`. An error is returned
// if `rawPublicDnsZoneResourceID` cannot be parsed.
// The validations that are performed are:
//   - The Public DNS Zone resource id is a valid Azure Resource ID
//   - The resource type of the  Public DNS Zone resource id is the
//     expected one for Azure Network Security Groups
func ParsePublicDNSZoneResourceID(rawPublicDNSZoneResourceID string) (azcorearm.ResourceID, error) {
	publicDNSZoneResourceTypeStr := "Microsoft.Network/dnsZones"
	publicDNSZoneResourceType, err := azcorearm.ParseResourceType(publicDNSZoneResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", publicDNSZoneResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawPublicDNSZoneResourceID, publicDNSZoneResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseChildSubnetResourceID parses an Azure Subnet Resource Name `subnetName`
// string together with an Azure VNet Resource ID `rawVnetResourceID`. The
// returned ResourceID represents an Azure Subnet whose name is `subnetName` and
// that belongs to the Azure Subnet represented by `vnetResourceID`. An error
// is returned if they cannot be parse.
// The validations that are performed are:
//   - The vnet resource id is a valid Azure Resource ID
//   - The resource type of the vnet resource id is the expected one for Azure
//     VNets.
func ParseChildSubnetResourceID(subnetName string, rawVnetResourceID string) (azcorearm.ResourceID, error) {
	vnetResource, err := ParseVnetResourceID(rawVnetResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	subnetResourceID := fmt.Sprintf("%s/subnets/%s", vnetResource.String(), subnetName)
	res, err := ParseSubnetResourceID(subnetResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// VnetResourceIDFromSubnetResourceID attempts to parse an Azure Subnet Resource ID
// from a Azure VNet Resource ID string specified in `subnetResourceID`. An error
// is returned if it cannot be parsed.
// The validations that are performed are:
// - The subnet resource id is a valid Azure Resource ID
//   - The resource type of the subnet resource id is the expected one for Azure
//     VNets.
func ParseVnetResourceIDFromSubnetResourceID(subnetResourceID string) (azcorearm.ResourceID, error) {
	subnetResource, err := ParseSubnetResourceID(subnetResourceID)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	if subnetResource.Parent == nil {
		return azcorearm.ResourceID{}, fmt.Errorf("subnet resource id has no parent resource id")
	}

	res, err := ParseVnetResourceID(subnetResource.Parent.String())
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseUserAssignedManagedIdentity parses an Azure User-Assigned Managed
// Identity Resource string specified in `rawUserAssignedManagedIdentity`.
// An error is returned if `rawUserAssignedManagedIdentity` cannot be parsed.
// The validations that are performed are:
//   - The User-Assigned Managed Identity resource id is a valid Azure Resource ID
//   - The resource type of the  User-Assigned Managed Identity resource id is the
//     expected one for Azure User-Assigned Managed Identities
func ParseUserAssignedManagedIdentity(rawUserAssignedManagedIdentity string) (azcorearm.ResourceID, error) {
	userAssignedManagedIdentityTypeStr := "Microsoft.ManagedIdentity/userAssignedIdentities"
	userAssignedManagedIdentityType, err := azcorearm.ParseResourceType(userAssignedManagedIdentityTypeStr)
	if err != nil {
		return azcorearm.ResourceID{},
			fmt.Errorf("error parsing resource type '%s': %w", userAssignedManagedIdentityTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawUserAssignedManagedIdentity, userAssignedManagedIdentityType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseACRResourceID parses an Azure Container Registry
// Resource string specified in `rawAcrResourceID`.
// An error is returned if `rawAcrResourceID` cannot be parsed.
// The validations that are performed are:
//   - The Azure Container Registry resource id is a valid Azure Resource ID
//   - The resource type of the Azure Container Registry resource id is the
//     expected one for Azure Container Registries
func ParseACRResourceID(rawACRResourceID string) (azcorearm.ResourceID, error) {
	acrResourceTypeStr := "Microsoft.ContainerRegistry/registries"
	acrResourceType, err := azcorearm.ParseResourceType(acrResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", acrResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawACRResourceID, acrResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

func ParseRoleDefinitionResourceID(rawRoleDefinitionResourceID string) (azcorearm.ResourceID, error) {
	roleDefinitionResourceTypeStr := "Microsoft.Authorization/roleDefinitions"
	roleDefinitionResourceType, err := azcorearm.ParseResourceType(roleDefinitionResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", roleDefinitionResourceTypeStr, err)
	}

	res, err := azcorearm.ParseResourceID(rawRoleDefinitionResourceID)
	if err != nil {
		return azcorearm.ResourceID{},
			fmt.Errorf("'%s' is not a valid Azure Resource ID: %w", rawRoleDefinitionResourceID, err)
	}

	resourceType := res.ResourceType
	if !strings.EqualFold(resourceType.String(), roleDefinitionResourceType.String()) {
		return azcorearm.ResourceID{},
			fmt.Errorf("'%s' is not a valid '%s' Resource ID", rawRoleDefinitionResourceID, roleDefinitionResourceType)
	}

	return *res, nil
}

// ParseResourceGroupResourceID parses an Azure Resource Group Resource
// string specified in `rawResourceGroupResourceID`. An error is returned
// if `rawResourceGroupResourceID` cannot be parsed.
// The validations that are performed are:
//   - The resource group resource id is a valid Azure Resource ID
//   - The resource type of the resource group resource id is the expected one
//     for Azure Resource Groups.
func ParseResourceGroupResourceID(rawResourceGroupResourceID string) (azcorearm.ResourceID, error) {
	resourceGroupResourceTypeStr := "Microsoft.Resources/resourceGroups"
	resourceGroupResourceType, err := azcorearm.ParseResourceType(resourceGroupResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", resourceGroupResourceTypeStr, err)
	}

	res, err := azcorearm.ParseResourceID(rawResourceGroupResourceID)
	if err != nil {
		return azcorearm.ResourceID{},
			fmt.Errorf("'%s' is not a valid Azure Resource ID: %w", rawResourceGroupResourceID, err)
	}

	resourceType := res.ResourceType
	if !strings.EqualFold(resourceType.String(), resourceGroupResourceType.String()) {
		return azcorearm.ResourceID{},
			fmt.Errorf("'%s' is not a valid '%s' Resource ID", rawResourceGroupResourceID, resourceGroupResourceType)
	}

	return *res, nil
}

func ParseDiskEncryptionSetResourceID(rawDiskEncryptionSetResourceID string) (azcorearm.ResourceID, error) {
	diskEncryptionSetResourceTypeStr := "Microsoft.Compute/diskEncryptionSets"
	diskEncryptionSetResourceType, err := azcorearm.ParseResourceType(diskEncryptionSetResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w",
			diskEncryptionSetResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawDiskEncryptionSetResourceID, diskEncryptionSetResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseKeyVaultResourceID parses an Azure Key Vault Resource
// string specified in `rawKeyVaultResourceID`. An error is returned
// if `rawKeyVaultResourceID` cannot be parsed.
// The validations that are performed are:
//   - The key vault resource id is a valid Azure Resource ID
//   - The resource type of the key vault resource id is the expected one
//     for Azure Key Vaults.
func ParseKeyVaultResourceID(rawKeyVaultResourceID string) (azcorearm.ResourceID, error) {
	keyVaultResourceTypeStr := "Microsoft.KeyVault/vaults"
	keyVaultResourceType, err := azcorearm.ParseResourceType(keyVaultResourceTypeStr)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf("error parsing resource type '%s': %w", keyVaultResourceTypeStr, err)
	}

	res, err := ParseResourceGroupScopedResourceID(rawKeyVaultResourceID, keyVaultResourceType)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}

// ParseAksManagementClusterResourceID parses an Azure Container Service Managed Cluster,
// (also known as AKS Cluster) string specified in `rawContainerServiceManagedClusterResourceID`.
// An error is returned if `rawContainerServiceManagedClusterResourceID` cannot be parsed.
// The validations that are performed are:
//   - The container service managed cluster resource id is a valid Azure Resource ID
//   - The resource type of the container service managed cluster resource id is the
//     expected one for Azure Container Service Managed Clusters.
func ParseContainerServiceManagedClusterResourceID(
	rawContainerServiceManagedClusterResourceID string,
) (azcorearm.ResourceID, error) {
	containerServiceManagedClusterResourceTypeStr := "Microsoft.ContainerService/managedClusters"
	containerServiceManagedClusterResourceType, err := azcorearm.ParseResourceType(
		containerServiceManagedClusterResourceTypeStr,
	)
	if err != nil {
		return azcorearm.ResourceID{}, fmt.Errorf(
			"error parsing resource type '%s': %w", containerServiceManagedClusterResourceTypeStr, err,
		)
	}

	res, err := ParseResourceGroupScopedResourceID(
		rawContainerServiceManagedClusterResourceID, containerServiceManagedClusterResourceType,
	)
	if err != nil {
		return azcorearm.ResourceID{}, err
	}

	return res, nil
}
