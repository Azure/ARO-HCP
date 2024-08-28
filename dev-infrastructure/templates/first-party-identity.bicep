@description('Azure Region Location')
param location string = resourceGroup().location

@description('Name of the Key Vault Certificate Officer Managed Identity')
param kvCertOfficerManagedIdentityName string

@description('The name of the global key vault')
param globalKeyVaultName string

module firstPartyIdentity '../modules/first-party-identity.bicep' = {
  name: 'first-party-identity'
  params: {
    location: location
    kvCertOfficerManagedIdentityName: kvCertOfficerManagedIdentityName
    globalKeyVaultName: globalKeyVaultName
  }
}
