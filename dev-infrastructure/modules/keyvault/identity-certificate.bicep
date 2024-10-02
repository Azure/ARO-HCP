@description('The name of the key vault')
param keyVaultName string

@description('UTC now')
param now string = utcNow('F')

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Name of the Key Vault Certificate Officer Managed Identity')
param kvCertOfficerManagedIdentityId string

@description('The name of the certificate')
param certName string

@description('The subject name of the certificate')
param subjectName string

@description('The DNS names of the certificate')
param dnsNames string

@description('The validity of the certificate in months')
param validityInMonths int = 12

//
// C E R T I F I C A T E   C R E A T I O N
//

resource newCertwithRotationKV 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: guid(keyVaultName, certName)
  location: location
  kind: 'AzurePowerShell'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${kvCertOfficerManagedIdentityId}': {}
    }
  }
  properties: {
    azPowerShellVersion: '7.5.0'
    arguments: ' -VaultName ${keyVaultName} -ValidityInMonths ${validityInMonths} -IssuerName "Self" -CertName ${certName} -SubjectName ${subjectName} -DnsNames ${dnsNames}'
    scriptContent: loadTextContent('../../scripts/key-vault-cert.ps1')
    forceUpdateTag: now
    cleanupPreference: 'Always'
    retentionInterval: 'P1D'
    timeout: 'PT5M'
  }
}
