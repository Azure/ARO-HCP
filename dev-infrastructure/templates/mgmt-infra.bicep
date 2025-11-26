@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('Defines if the CX KeyVault is private')
param cxKeyVaultPrivate bool

@description('Defines if the CX KeyVault has soft delete enabled')
param cxKeyVaultSoftDelete bool

// CX KV tagging
param cxKeyVaultTagName string
param cxKeyVaultTagValue string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('Defines if the MSI KeyVault is private')
param msiKeyVaultPrivate bool

@description('Defines if the MSI KeyVault has soft delete enabled')
param msiKeyVaultSoftDelete bool

// MSI KV tagging
param msiKeyVaultTagName string
param msiKeyVaultTagValue string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('Defines if the MGMT KeyVault is private')
param mgmtKeyVaultPrivate bool

@description('Defines if the MGMT KeyVault has soft delete enabled')
param mgmtKeyVaultSoftDelete bool

// MGMT KV tagging
param mgmtKeyVaultTagName string
param mgmtKeyVaultTagValue string

@description('KV certificate officer principal ID')
param kvCertOfficerPrincipalId string

@description('MSI that will be used during pipeline runs')
param globalMSIId string

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId string = ''

// Storage Account for HCP Backups
@minLength(3)
// @maxLength(24) Fails on EV2 pipelines, probably because the EV2 placeholder is longer than 24.
param hcpBackupsStorageAccountName string
param hcpBackupsStorageAccountContainerName string = 'backups'
param hcpBackupsStorageAccountZoneRedundantMode string = 'Auto'
param hcpBackupsStorageAccountPublic bool = true

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, globalMSIId, readerRoleId)
  properties: {
    principalId: reference(globalMSIId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
//   K E Y V A U L T S
//

module cxKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'cx-kv-${uniqueString(cxKeyVaultName)}'
  params: {
    location: location
    keyVaultName: cxKeyVaultName
    private: cxKeyVaultPrivate
    enableSoftDelete: cxKeyVaultSoftDelete
    tagKey: cxKeyVaultTagName
    tagValue: cxKeyVaultTagValue
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

module cxKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, kvCertOfficerPrincipalId, role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalId: kvCertOfficerPrincipalId
    }
    dependsOn: [
      cxKeyVault
    ]
  }
]

output cxKeyVaultUrl string = cxKeyVault.outputs.kvUrl

module msiKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'msi-kv-${uniqueString(msiKeyVaultName)}'
  params: {
    location: location
    keyVaultName: msiKeyVaultName
    private: msiKeyVaultPrivate
    enableSoftDelete: msiKeyVaultSoftDelete
    tagKey: msiKeyVaultTagName
    tagValue: msiKeyVaultTagValue
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

output msiKeyVaultUrl string = msiKeyVault.outputs.kvUrl

module mgmtKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: 'mgmt-kv-${uniqueString(mgmtKeyVaultName)}'
  params: {
    location: location
    keyVaultName: mgmtKeyVaultName
    private: mgmtKeyVaultPrivate
    enableSoftDelete: mgmtKeyVaultSoftDelete
    tagKey: mgmtKeyVaultTagName
    tagValue: mgmtKeyVaultTagValue
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

module mgmtKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(mgmtKeyVaultName, kvCertOfficerPrincipalId, role)
    params: {
      keyVaultName: mgmtKeyVaultName
      roleName: role
      managedIdentityPrincipalId: kvCertOfficerPrincipalId
    }
    dependsOn: [
      mgmtKeyVault
    ]
  }
]

output mgmtKeyVaultUrl string = mgmtKeyVault.outputs.kvUrl

//
// H C P   B A C K U P S   S T O R A G E
//

module hcpBackupsStorage '../modules/hcp-backups/storage.bicep' = {
  name: 'hcp-backups-storage'
  params: {
    storageAccountName: hcpBackupsStorageAccountName
    location: location
    containerName: hcpBackupsStorageAccountContainerName
    zoneRedundantMode: hcpBackupsStorageAccountZoneRedundantMode
    public: hcpBackupsStorageAccountPublic
  }
}

output hcpBackupsStorageAccountId string = hcpBackupsStorage.outputs.storageAccountId
