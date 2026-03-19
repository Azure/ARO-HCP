@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the MSI used for Key Vault operations')
param globalMSIName string

@description('The name of the key vault')
param keyVaultName string

@description('Number of MSI mock identities to create for throttle distribution')
param msiMockPoolSize int = 20

@description('Base name for pooled MSI mock certificates')
param msiMockPoolCertBaseName string = 'msiMockPoolCert'

@description('Base DNS for pooled MSI mock certificates')
param msiMockPoolCertBaseDns string = 'msimockpool.hcp.osadev.cloud'

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

//
// M S I   R P   M O C K   I D E N T I T Y   P O O L
//
// Additional MSI mock identities to distribute ARM read load across multiple
// service principals, avoiding per-principal subscription-level throttling
// during concurrent E2E test runs.

module msiRPMockIdentityPoolLeader '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'msi-mock-identity-pool-0'
  params: {
    location: location
    keyVaultManagedIdentityId: globalMSI.id
    keyVaultName: keyVaultName
    certName: '${msiMockPoolCertBaseName}-0'
    subjectName: 'CN=0.${msiMockPoolCertBaseDns}'
    dnsNames: ['0.${msiMockPoolCertBaseDns}']
    issuerName: 'Self'
    validityInMonths: 120
  }
}

module msiRPMockIdentityPool '../modules/keyvault/key-vault-cert.bicep' = [
  for i in range(1, msiMockPoolSize - 1): {
    name: 'msi-mock-identity-pool-${i}'
    dependsOn: [msiRPMockIdentityPoolLeader]
    params: {
      location: location
      keyVaultManagedIdentityId: globalMSI.id
      keyVaultName: keyVaultName
      certName: '${msiMockPoolCertBaseName}-${i}'
      subjectName: 'CN=${i}.${msiMockPoolCertBaseDns}'
      dnsNames: ['${i}.${msiMockPoolCertBaseDns}']
      issuerName: 'Self'
      validityInMonths: 120
    }
  }
]
