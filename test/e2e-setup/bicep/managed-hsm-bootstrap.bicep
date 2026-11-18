targetScope = 'resourceGroup'

// Provisions an un-activated Managed HSM for the etcd mHSM e2e test. Activation
// (security-domain download), data-plane role assignments and key creation run
// from the test process: a deploymentScript would add an ACI dependency that
// intermittently fails with DeploymentScriptACIProvisioningTimeout (AROSLSRE-2510).

@description('Globally unique Managed HSM name (3-24 alphanumeric characters or hyphens).')
param hsmName string

@description('Azure region for all resources.')
param location string = resourceGroup().location

@minLength(1)
@description('Entra object IDs that should be initial Managed HSM administrators. The principal that activates the HSM must be included.')
param adminObjectIds array

@description('Enable irreversible purge protection. Recommended for production; leave false for disposable testing.')
param enablePurgeProtection bool = false

@minValue(7)
@maxValue(90)
@description('Soft-delete retention period in days. Azure Managed HSM requires at least 7 days.')
param softDeleteRetentionInDays int = 7

resource managedHsm 'Microsoft.KeyVault/managedHSMs@2025-05-01' = {
  name: hsmName
  location: location
  sku: {
    family: 'B'
    name: 'Standard_B1'
  }
  properties: {
    createMode: 'default'
    enablePurgeProtection: enablePurgeProtection
    enableSoftDelete: true
    initialAdminObjectIds: adminObjectIds
    publicNetworkAccess: 'Enabled'
    softDeleteRetentionInDays: softDeleteRetentionInDays
    tenantId: tenant().tenantId
  }
}

output managedHsmUri string = managedHsm.properties.hsmUri
