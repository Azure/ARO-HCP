@description('Azure Region Location')
param location string = resourceGroup().location

@description('The resource ID of the managed identity that will be used for Key Vault operations')
param aroDevopsMsiId string

@description('The name of the key vault')
param keyVaultName string

@description('Global resource group name')
param globalResourceGroupName string = 'global'

//
// F I R S T   P A R T Y   I D E N T I T Y
//

module firstPartyIdentity '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'first-party-identity'
  params: {
    location: location
    keyVaultManagedIdentityId: aroDevopsMsiId
    keyVaultName: keyVaultName
    certName: 'firstPartyCert2'
    subjectName: 'CN=firstparty.hcp.osadev.cloud'
    issuerName: 'Self'
    dnsNames: ['firstparty.hcp.osadev.cloud']
    validityInMonths: 120
  }
}

resource customRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, 'dev-first-party-mock')
  properties: {
    roleName: 'dev-first-party-mock'
    description: 'ARO HCP Dev Role for mock 1p service principal'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.Resources/subscriptions/resourceGroups/read'
          'Microsoft.Resources/subscriptions/resourceGroups/write'
          'Microsoft.Authorization/*/action'
        ]
        notActions: []
      }
    ]
    assignableScopes: [
      subscription().id
      subscriptionResourceId('Microsoft.Resources/resourceGroups/', globalResourceGroupName)
    ]
  }
}

//
// A R M   H E L P E R   I D E N T I T Y
//

module armHelperIdentity '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'arm-helper-identity'
  params: {
    location: location
    keyVaultManagedIdentityId: aroDevopsMsiId
    keyVaultName: keyVaultName
    certName: 'armHelperCert2'
    subjectName: 'CN=armhelper.hcp.osadev.cloud'
    dnsNames: ['armhelper.hcp.osadev.cloud']
    issuerName: 'Self'
    validityInMonths: 120
  }
}

//
// M S I   R P   M O CK   I D E N T I T Y
//

module msiRPMockIdentity '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'msi-mock-identity'
  params: {
    location: location
    keyVaultManagedIdentityId: aroDevopsMsiId
    keyVaultName: keyVaultName
    certName: 'msiMockCert2'
    subjectName: 'CN=msimock.hcp.osadev.cloud'
    dnsNames: ['msimock.hcp.osadev.cloud']
    issuerName: 'Self'
    validityInMonths: 120
  }
}

resource msiCustomRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, 'dev-msi-mock')
  properties: {
    roleName: 'dev-msi-mock'
    description: 'ARO HCP Dev Role for MSI mock principal'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.Network/virtualNetworks/read'
          'Microsoft.Network/virtualNetworks/subnets/read'
          'Microsoft.Network/virtualNetworks/subnets/write'
          'Microsoft.Network/virtualNetworks/subnets/join/action'
          'Microsoft.Network/routeTables/read'
          'Microsoft.Network/routeTables/join/action'
          'Microsoft.Network/natGateways/join/action'
          'Microsoft.Network/natGateways/read'
          'Microsoft.Network/networkSecurityGroups/read'
          'Microsoft.Network/networkSecurityGroups/write'
          'Microsoft.Network/networkSecurityGroups/join/action'
          'Microsoft.ManagedIdentity/userAssignedIdentities/read'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete'
        ]
        notActions: []
      }
    ]
    assignableScopes: [
      subscription().id
      subscriptionResourceId('Microsoft.Resources/resourceGroups/', globalResourceGroupName)
    ]
  }
}
