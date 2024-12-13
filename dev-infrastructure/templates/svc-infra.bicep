@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resourcegroup for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The location of the resourcegroup for the service keyvault')
param serviceKeyVaultLocation string = resourceGroup().location

@description('Soft delete setting for service keyvault')
param serviceKeyVaultSoftDelete bool = true

@description('If true, make the service keyvault private and only accessible by the svc cluster via private link.')
param serviceKeyVaultPrivate bool = true

@description('MSI that will be used to run the deploymentScript')
param aroDevopsMsiId string

@description('Frontend Certificate Name')
param certName string

@description('This is a regional DNS zone')
param regionalDNSZoneName string

//
//   K E Y V A U L T S
//

module serviceKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-svcs-kv'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    location: serviceKeyVaultLocation
    keyVaultName: serviceKeyVaultName
    private: serviceKeyVaultPrivate
    enableSoftDelete: serviceKeyVaultSoftDelete
    purpose: 'service'
  }
}
output svcKeyVaultName string = serviceKeyVault.outputs.kvName

//
//   C E R T I F I C A T E   C R E A T I O N
//

var clientAuthenticationName = 'frontend.${regionalDNSZoneName}'

module clientCertificate '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'frontend-cert-${uniqueString(certName)}'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVault.outputs.kvName
    subjectName: 'CN=frontend'
    certName: certName
    keyVaultManagedIdentityId: aroDevopsMsiId
    dnsNames: [
      clientAuthenticationName
    ]
    issuerName: 'Self' // TODO: Change to OneCertV2-PublicCA when we get the issuer approved.
  }
}
