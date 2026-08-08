@description('Name of the AKS cluster for OIDC issuer lookup')
param clusterName string

@description('Name of the CX Key Vault')
param cxKeyVaultName string

@description('Resource group of the CX Key Vault')
param cxKeyVaultResourceGroup string

@description('Name of the MI Key Vault')
param miKeyVaultName string

@description('Resource group of the MI Key Vault')
param miKeyVaultResourceGroup string

@description('Name of the OCP ACR')
param ocpAcrName string

@description('Resource group of the OCP ACR')
param ocpAcrResourceGroup string

param location string = resourceGroup().location

var miName = 'cspr-cleaner-mi'

resource aksCluster 'Microsoft.ContainerService/managedClusters@2025-07-02-preview' existing = {
  name: clusterName
}

resource cleanerMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: miName
  location: location
}

resource fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = {
  parent: cleanerMI
  name: 'resource-cleaner-fedcred'
  properties: {
    audiences: [
      'api://AzureADTokenExchange'
    ]
    issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
    subject: 'system:serviceaccount:resource-cleaner:resource-cleaner-cronjob'
  }
}

module cxKvCertOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'cleaner-cx-kv-cert-officer'
  scope: resourceGroup(cxKeyVaultResourceGroup)
  params: {
    keyVaultName: cxKeyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalIds: [cleanerMI.properties.principalId]
  }
}

module cxKvSecretOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'cleaner-cx-kv-secret-officer'
  scope: resourceGroup(cxKeyVaultResourceGroup)
  params: {
    keyVaultName: cxKeyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalIds: [cleanerMI.properties.principalId]
  }
}

module miKvSecretOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'cleaner-mi-kv-secret-officer'
  scope: resourceGroup(miKeyVaultResourceGroup)
  params: {
    keyVaultName: miKeyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalIds: [cleanerMI.properties.principalId]
  }
}

module acrDelete '../modules/acr/acr-permissions.bicep' = {
  name: 'cleaner-acr-delete'
  scope: resourceGroup(ocpAcrResourceGroup)
  params: {
    acrName: ocpAcrName
    principalIds: [cleanerMI.properties.principalId]
    grantDeleteAccess: true
  }
}

output principalId string = cleanerMI.properties.principalId
output clientId string = cleanerMI.properties.clientId
output tenantId string = tenant().tenantId
