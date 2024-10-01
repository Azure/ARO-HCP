@description('Azure Region Location')
param location string = resourceGroup().location

@description('Name of the Key Vault Certificate Officer Managed Identity')
param kvCertOfficerManagedIdentityName string

@description('The name of the key vault')
param keyVaultName string

@description('Global resource group name')
param globalResourceGroupName string = 'global'

module firstPartyIdentity '../modules/first-party-identity.bicep' = {
  name: 'first-party-identity'
  params: {
    location: location
    kvCertOfficerManagedIdentityName: kvCertOfficerManagedIdentityName
    keyVaultName: keyVaultName
    globalResourceGroupName: globalResourceGroupName
  }
  scope: resourceGroup(globalResourceGroupName)
}
