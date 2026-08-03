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

package denyassignments

func resourcesActions() []string {
	return []string{
		"Microsoft.Resources/subscriptions/resourceGroups/delete",
		"Microsoft.Resources/subscriptions/resourceGroups/read",
		"Microsoft.Resources/subscriptions/resourceGroups/write",
		"Microsoft.Resources/deployments/delete",
		"Microsoft.Resources/deployments/write",
	}
}

func resourcesNotActions() []string {
	return []string{
		"Microsoft.Resources/tags/*",
	}
}

func computeActions() []string {
	return []string{
		"Microsoft.Compute/availabilitySets/delete",
		"Microsoft.Compute/availabilitySets/write",
		"Microsoft.Compute/disks/beginGetAccess/action",
		"Microsoft.Compute/disks/delete",
		"Microsoft.Compute/disks/endGetAccess/action",
		"Microsoft.Compute/disks/write",
		"Microsoft.Compute/images/delete",
		"Microsoft.Compute/images/write",
		"Microsoft.Compute/snapshots/beginGetAccess/action",
		"Microsoft.Compute/snapshots/delete",
		"Microsoft.Compute/snapshots/endGetAccess/action",
		"Microsoft.Compute/snapshots/write",
		"Microsoft.Compute/availabilitySets/read",
		"Microsoft.Compute/diskEncryptionSets/read",
		"Microsoft.Compute/disks/read",
		"Microsoft.Compute/locations/DiskOperations/read",
		"Microsoft.Compute/locations/operations/read",
		"Microsoft.Compute/snapshots/read",
		"Microsoft.Compute/virtualMachineScaleSets/read",
		"Microsoft.Compute/virtualMachineScaleSets/virtualMachines/read",
		"Microsoft.Compute/virtualMachineScaleSets/virtualMachines/write",
		"Microsoft.Compute/virtualMachines/delete",
		"Microsoft.Compute/virtualMachines/read",
		"Microsoft.Compute/virtualMachines/write",
	}
}

func computeNotActions() []string {
	return []string{
		"Microsoft.Compute/disks/beginGetAccess/action",
		"Microsoft.Compute/disks/endGetAccess/action",
		"Microsoft.Compute/disks/write",
		"Microsoft.Compute/snapshots/beginGetAccess/action",
		"Microsoft.Compute/snapshots/delete",
		"Microsoft.Compute/snapshots/endGetAccess/action",
		"Microsoft.Compute/snapshots/write",
	}
}

func resourceHealthActions() []string {
	return []string{
		"Microsoft.ResourceHealth/events/action",
	}
}

func apiManagementActions() []string {
	return []string{
		"Microsoft.ApiManagement/service/groups/delete",
		"Microsoft.ApiManagement/service/groups/read",
		"Microsoft.ApiManagement/service/groups/write",
		"Microsoft.ApiManagement/service/workspaces/tags/read",
		"Microsoft.ApiManagement/service/workspaces/tags/write",
	}
}

func storageActions() []string {
	return []string{
		"Microsoft.Storage/storageAccounts/read",
		"Microsoft.Storage/storageAccounts/write",
		"Microsoft.Storage/storageAccounts/delete",
		"Microsoft.Storage/storageAccounts/listKeys/action",
		"Microsoft.Storage/storageAccounts/regeneratekey/action",
		"Microsoft.Storage/storageAccounts/blobServices/read",
		"Microsoft.Storage/storageAccounts/blobServices/write",
		"Microsoft.Storage/storageAccounts/blobServices/containers/delete",
		"Microsoft.Storage/storageAccounts/blobServices/containers/read",
		"Microsoft.Storage/storageAccounts/blobServices/containers/write",
		"Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey/action",
		"Microsoft.Storage/storageAccounts/fileServices/read",
		"Microsoft.Storage/storageAccounts/fileServices/write",
		"Microsoft.Storage/storageAccounts/fileServices/shares/read",
		"Microsoft.Storage/storageAccounts/fileServices/shares/write",
		"Microsoft.Storage/storageAccounts/fileServices/shares/delete",
		"Microsoft.Storage/storageAccounts/PrivateEndpointConnectionsApproval/action",
		"Microsoft.Storage/operations/read",
	}
}

func storageDataActions() []string {
	return []string{
		"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
		"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
		"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete",
		"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/add/action",
		"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/move/action",
		"Microsoft.Storage/storageAccounts/fileServices/fileshares/files/read",
		"Microsoft.Storage/storageAccounts/fileServices/fileshares/files/write",
		"Microsoft.Storage/storageAccounts/fileServices/fileshares/files/delete",
	}
}

func managedIdentityActions() []string {
	return []string{
		"Microsoft.ManagedIdentity/userAssignedIdentities/assign/action",
		"Microsoft.ManagedIdentity/userAssignedIdentities/read",
		"Microsoft.ManagedIdentity/userAssignedIdentities/write",
		"Microsoft.ManagedIdentity/userAssignedIdentities/delete",
		"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read",
		"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write",
		"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete",
	}
}

func keyVaultActions() []string {
	return []string{
		"Microsoft.KeyVault/vaults/deploy/action",
	}
}

func keyVaultDataActions() []string {
	return []string{
		"Microsoft.KeyVault/vaults/keys/read",
		"Microsoft.KeyVault/vaults/keys/update/action",
		"Microsoft.KeyVault/vaults/keys/backup/action",
		"Microsoft.KeyVault/vaults/keys/encrypt/action",
		"Microsoft.KeyVault/vaults/keys/decrypt/action",
		"Microsoft.KeyVault/vaults/keys/wrap/action",
		"Microsoft.KeyVault/vaults/keys/unwrap/action",
		"Microsoft.KeyVault/vaults/keys/sign/action",
		"Microsoft.KeyVault/vaults/keys/verify/action",
	}
}

func containerServiceActions() []string {
	return []string{
		"Microsoft.ContainerService/managedClusters/agentPools/write",
		"Microsoft.ContainerService/managedClusters/delete",
		"Microsoft.ContainerService/managedClusters/write",
	}
}

func networkVirtualNetworksManagementActions() []string {
	return []string{
		"Microsoft.Network/virtualNetworks/delete",
		"Microsoft.Network/virtualNetworks/write",
		"Microsoft.Network/virtualNetworks/subnets/delete",
		"Microsoft.Network/virtualNetworks/subnets/write",
	}
}

func networkVirtualNetworksReadActions() []string {
	return []string{
		"Microsoft.Network/virtualNetworks/read",
		"Microsoft.Network/virtualNetworks/subnets/read",
		"Microsoft.Network/virtualNetworks/virtualNetworkPeerings/read",
	}
}

func networkVirtualNetworksJoinActions() []string {
	return []string{
		"Microsoft.Network/virtualNetworks/join/action",
		"Microsoft.Network/virtualNetworks/subnets/join/action",
	}
}

func networkLoadBalancingPublicIPAndRouteTablesActions() []string {
	return []string{
		"Microsoft.Network/loadBalancers/inboundNATRules/join/action",
		"Microsoft.Network/loadBalancers/loadBalancingRules/read",
		"Microsoft.Network/loadBalancers/read",
		"Microsoft.Network/loadBalancers/write",
		"Microsoft.Network/loadBalancers/delete",
		"Microsoft.Network/loadBalancers/backendAddressPools/join/action",
		"Microsoft.Network/loadBalancers/backendAddressPools/read",
		"Microsoft.Network/loadBalancers/backendAddressPools/write",
		"Microsoft.Network/loadBalancers/frontendIPConfigurations/join/action",
		"Microsoft.Network/loadBalancers/inboundNatRules/join/action",
		"Microsoft.Network/loadBalancers/probes/join/action",
		"Microsoft.Network/virtualNetworks/joinLoadBalancer/action",
		"Microsoft.Network/publicIPAddresses/read",
		"Microsoft.Network/publicIPAddresses/write",
		"Microsoft.Network/publicIPAddresses/delete",
		"Microsoft.Network/publicIPAddresses/join/action",
		"Microsoft.Network/publicIPPrefixes/join/action",
		"Microsoft.Network/routeTables/read",
		"Microsoft.Network/routeTables/write",
		"Microsoft.Network/routeTables/delete",
		"Microsoft.Network/routeTables/join/action",
	}
}

func networkPrivateConnectivityActions() []string {
	return []string{
		"Microsoft.Network/privatelinkservices/delete",
		"Microsoft.Network/privatelinkservices/read",
		"Microsoft.Network/privatelinkservices/write",
		"Microsoft.Network/privateEndpoints/read",
		"Microsoft.Network/privateEndpoints/write",
		"Microsoft.Network/privateEndpoints/delete",
		"Microsoft.Network/privateDnsOperationStatuses/read",
		"Microsoft.Network/privateDnsZones/join/action",
		"Microsoft.Network/privateDnsZones/read",
		"Microsoft.Network/privateDnsZones/virtualNetworkLinks/read",
		"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read",
		"Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write",
		"Microsoft.Network/privateDnsZones/write",
		"Microsoft.Network/privateDnsZones/delete",
		"Microsoft.Network/privateDnsZones/A/write",
		"Microsoft.Network/privateDnsZones/A/delete",
		"Microsoft.Network/privateDnsZones/virtualNetworkLinks/write",
		"Microsoft.Network/privateDnsZones/virtualNetworkLinks/delete",
		"Microsoft.Network/dnsZones/write",
		"Microsoft.Network/dnsZones/delete",
		"Microsoft.Network/dnsZones/A/write",
		"Microsoft.Network/dnsZones/A/delete",
		"Microsoft.Network/locations/operations/read",
	}
}

func networkSecurityGroupsAndNatGatewaysActions() []string {
	return []string{
		"Microsoft.Network/networkSecurityGroups/read",
		"Microsoft.Network/networkSecurityGroups/write",
		"Microsoft.Network/networkSecurityGroups/delete",
		"Microsoft.Network/networkSecurityGroups/join/action",
		"Microsoft.Network/natGateways/join/action",
		"Microsoft.Network/natGateways/read",
	}
}

func applicationSecurityGroupsActions() []string {
	return []string{
		"Microsoft.Network/applicationSecurityGroups/read",
		"Microsoft.Network/applicationSecurityGroups/write",
		"Microsoft.Network/applicationSecurityGroups/delete",
		"Microsoft.Network/applicationSecurityGroups/joinNetworkSecurityRule/action",
		"Microsoft.Network/applicationSecurityGroups/joinIpConfiguration/action",
	}
}

func networkInterfacesActions() []string {
	return []string{
		"Microsoft.Network/networkInterfaces/read",
		"Microsoft.Network/networkInterfaces/write",
		"Microsoft.Network/networkInterfaces/delete",
		"Microsoft.Network/networkInterfaces/join/action",
		"Microsoft.Network/networkInterfaces/loadBalancers/read",
		"Microsoft.Network/networkInterfaces/effectiveRouteTable/action",
	}
}

func networkInterfacesNotActions() []string {
	return []string{
		"Microsoft.Network/networkInterfaces/effectiveRouteTable/action",
		"Microsoft.Network/networkSecurityGroups/join/action",
	}
}

func networkPoliciesAndServicesActions() []string {
	return []string{
		"Microsoft.Network/serviceEndpointPolicies/read",
		"Microsoft.Network/serviceEndpointPolicies/write",
		"Microsoft.Network/serviceEndpointPolicies/delete",
		"Microsoft.Network/serviceEndpointPolicies/join/action",
		"Microsoft.Network/networkIntentPolicies/join/action",
		"Microsoft.Network/networkManagers/ipamPools/associateResourcesToPool/action",
	}
}

func bastionHostsActions() []string {
	return []string{
		"Microsoft.Network/bastionHosts/write",
		"Microsoft.Network/bastionHosts/delete",
	}
}

func denyAllOtherRPsActions() []string {
	return []string{
		"*/action",
		"*/delete",
		"*/write",
	}
}

func denyAllOtherRPsNotActions() []string {
	return []string{
		"Microsoft.Resources/*",
		"Microsoft.Compute/*",
		"Microsoft.Storage/*",
		"Microsoft.Network/*",
		"Microsoft.ManagedIdentity/*",
		"Microsoft.KeyVault/*",
		"Microsoft.Authorization/*",
		"Microsoft.ContainerService/*",
		"Microsoft.ResourceHealth/*",
		"Microsoft.ApiManagement/*",
		"Microsoft.Insights/*",
		"Microsoft.PolicyInsights/*",
	}
}
