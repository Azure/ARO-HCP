/*
  This module creates infrastructure required by Azure deployment scripts
  (Microsoft.Resources/deploymentScripts) when Azure policy blocks local auth
  on storage accounts (SFI-ID4.2.1).

  Deployment scripts need a storage account to store script content and output.
  By default, ARM auto-creates one with shared key auth, which policy now blocks.
  To work around this, we:
  1. Create a storage account with allowSharedKeyAccess=false
  2. Create a VNet + subnet delegated to Microsoft.ContainerInstance/containerGroups
     with a Microsoft.Storage service endpoint
  3. Grant the deployment MSIs the "Storage File Data Privileged Contributor" role

  The storage account uses public network access with Azure AD-only authentication
  (no shared keys). Network ACLs are not restricted to the subnet because ACI
  provisioning may fail to reach a subnet-restricted storage account during setup.

  Each deployment script must then specify both:
  - storageAccountSettings: { storageAccountName: <name> }
  - containerSettings: { subnetIds: [{ id: <subnetId> }] }
*/

@description('The name of the storage account for deployment scripts')
@minLength(3)
@maxLength(24)
param storageAccountName string

@description('The location of the resources')
param location string

@description('Principal IDs of the managed identities that will run deployment scripts and need access to this storage account')
param managedIdentityPrincipalIds array

@description('The name of the VNet to create for deployment scripts ACI containers')
param vnetName string = 'deployment-scripts-vnet'

@description('The address prefix for the deployment scripts VNet')
param vnetAddressPrefix string = '10.255.200.0/24'

@description('The address prefix for the deployment scripts subnet')
param subnetAddressPrefix string = '10.255.200.0/26'

// Storage File Data Privileged Contributor
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/storage#storage-file-data-privileged-contributor
var storageFileDataPrivilegedContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '69566ab7-960f-475b-8e7c-b3118f30c6bd'
)

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: vnetName
  location: location
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
    subnets: [
      {
        name: 'deployment-scripts'
        properties: {
          addressPrefix: subnetAddressPrefix
          serviceEndpoints: [
            {
              service: 'Microsoft.Storage'
            }
          ]
          delegations: [
            {
              name: 'Microsoft.ContainerInstance.containerGroups'
              properties: {
                serviceName: 'Microsoft.ContainerInstance/containerGroups'
              }
            }
          ]
        }
      }
    ]
  }
}

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  name: toLower(storageAccountName)
  location: location
  kind: 'StorageV2'
  sku: {
    name: 'Standard_LRS'
  }
  properties: {
    accessTier: 'Hot'
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
    allowBlobPublicAccess: false
    allowSharedKeyAccess: false
    publicNetworkAccess: 'Enabled'
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Allow'
    }
  }
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principalId in managedIdentityPrincipalIds: {
    name: guid(storageAccount.id, principalId, storageFileDataPrivilegedContributorRoleId)
    scope: storageAccount
    properties: {
      principalId: principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: storageFileDataPrivilegedContributorRoleId
    }
  }
]

output storageAccountName string = storageAccount.name
output subnetId string = vnet.properties.subnets[0].id
