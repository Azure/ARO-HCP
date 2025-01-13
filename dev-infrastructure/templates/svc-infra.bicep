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

@description('''
  This is a regional DNS zone name to hold records for ARO HCP service components,
  e.g. the RP
  ''')
param regionalSvcDNSZoneName string

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

// Tags the resource group
resource resourcegroupTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
    }
  }
}

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

module serviceKeyVaultDevopsCertOfficer '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: '${deployment().name}-svc-kv-cert-officer'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalId: reference(aroDevopsMsiId, '2023-01-31').principalId
  }
  dependsOn: [
    serviceKeyVault
  ]
}

output svcKeyVaultName string = serviceKeyVault.outputs.kvName
output svcKeyVaultUrl string = serviceKeyVault.outputs.kvUrl

//
//   C E R T I F I C A T E   C R E A T I O N
//

var clientAuthenticationName = 'frontend.${regionalSvcDNSZoneName}'

module clientCertificate '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'frontend-cert-${uniqueString(resourceGroup().name)}'
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
