param location string

@description('The VNET that should be tagged')
param vnetName string

@description('Enable swift')
param enableSwift bool

@description('The address space for the VNET')
param vnetAddressPrefix string

@description('The resource ID of the Swift-registered user-assigned managed identity granted RBAC here and assigned to the container group (scripts/swift-vnet.sh) that creates/tags the VNet')
param deploymentMsiId string

//
//  D E P L O Y   V N E T   W I T H O U T   S W I F T
//

// for non-swift deployments, we create the VNET regularly... so much faster
resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' = if (!enableSwift) {
  location: location
  name: vnetName
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
  }
}

//
//  D E P L O Y   V N E T   W I T H   S W I F T
//

// For Swift, the VNet is created and tagged (stampcreatorserviceinfo=true) by a managed-identity
// container group (launched by scripts/swift-vnet.sh) that runs AS the Swift-registered identity,
// since that write must come from an identity registered for Swift usage with the network RP. The
// management pipeline provisions this RBAC and runs that container before the cluster is built;
// this module keeps the same assignments idempotent for direct ARM deployments and declares the
// VNet as existing because the container (not this module) creates it.

module vnetRbac 'vnet-rbac.bicep' = if (enableSwift) {
  name: 'vnet-rbac'
  params: {
    deploymentMsiId: deploymentMsiId
  }
}

resource provisionedSwiftVnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = if (enableSwift) {
  // The container group creates this VNet after RBAC is ready; this module only declares it existing.
  name: vnetName
}

output vnetId string = enableSwift ? provisionedSwiftVnet.id : vnet.id
output vnetName string = enableSwift ? provisionedSwiftVnet.name : vnet.name
