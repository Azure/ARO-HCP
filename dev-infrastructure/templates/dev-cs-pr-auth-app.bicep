@description('Name of the AKS cluster')
param aksClusterName string

@description('Name of the service Key Vault')
param serviceKeyVaultName string

@description('Resource group of the service Key Vault')
param serviceKeyVaultResourceGroup string

resource aksCluster 'Microsoft.ContainerService/managedClusters@2025-07-02-preview' existing = {
  name: aksClusterName
}

module csAuthApp '../modules/entra/app.bicep' = {
  name: 'cs-pr-auth-app'
  params: {
    applicationName: 'cs-pr-authentication'
    uniqueName: toLower(replace('cs-pr-authentication', ' ', '-'))
    manageSp: true
  }
}

var contributorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b24988ac-6180-42a0-ab88-20f7382dd24c'
)

resource aksContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksCluster.id, 'cs-pr-authentication', contributorRoleDefinitionId)
  scope: aksCluster
  properties: {
    principalId: csAuthApp.outputs.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleDefinitionId
  }
}

module kvCertUser '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'cs-pr-auth-kv-cert-user'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Certificate User'
    managedIdentityPrincipalIds: [csAuthApp.outputs.principalId]
  }
}
