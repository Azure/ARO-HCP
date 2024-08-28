@description('The name of the global key vault')
param globalKeyVaultName string

@description('UTC now')
param now string = utcNow('F')

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Name of the Key Vault Certificate Officer Managed Identity')
param kvCertOfficerManagedIdentityName string

//
// C E R T I F I C A T E   O F F I C E R   M S I
//

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: globalKeyVaultName
}

resource kvCertOfficerManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: kvCertOfficerManagedIdentityName
  location: location
}

var keyVaultCertificateOfficerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'a4417e6f-fecd-4de8-b567-7b0420556985'
)

resource kvManagedIdentityRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: kv
  name: guid(kvCertOfficerManagedIdentity.id, keyVaultCertificateOfficerRoleId, kv.id)
  properties: {
    roleDefinitionId: keyVaultCertificateOfficerRoleId
    principalId: kvCertOfficerManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

//
// C E R T I F I C A T E   C R E A T I O N
//

resource newCertwithRotationKV 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: 'dev-first-party-mock-cert'
  location: location
  kind: 'AzurePowerShell'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${kvCertOfficerManagedIdentity.id}': {}
    }
  }
  properties: {
    azPowerShellVersion: '7.5.0'
    arguments: ' -VaultName ${globalKeyVaultName} -IssuerName "Self" -CertName "firstPartyMock" -SubjectName "CN=firstpartymock.hcp.osadev.cloud" -DnsNames "firstpartymock.hcp.osadev.cloud"'
    scriptContent: loadTextContent('../scripts/key-vault-cert.ps1')
    forceUpdateTag: now
    cleanupPreference: 'Always'
    retentionInterval: 'P1D'
    timeout: 'PT5M'
  }
}

//
// C U S T O M   R O L E  D E F I N I T I O N
//

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
      subscriptionResourceId('Microsoft.Resources/resourceGroups/', 'global')
      subscriptionResourceId('Microsoft.Resources/resourceGroups/', 'aro-hcp-dev-westus3-sc')
    ]
  }
}
