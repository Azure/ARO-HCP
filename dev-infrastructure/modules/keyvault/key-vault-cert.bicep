/*
Creating certificates in Azure Key Vault is not supported by Bicep yet.
This module leverages a deploymentscript to solve this for the time beeing.
Proudly stolen from https://github.com/Azure/bicep/discussions/8457
*/

param keyVaultName string
param certName string
param subjectName string
param issuerName string
param dnsNames array
param now string = utcNow('F')
param keyVaultManagedIdentityId string
param location string = resourceGroup().location
param force bool = false
var boolstring = force == false ? '$false' : '$true'
param validityInMonths int = 12

module certificateOfficerAccess 'keyvault-secret-access.bicep' = {
  name: 'kv-cert-officer-access-${keyVaultName}-${uniqueString(keyVaultManagedIdentityId)}'
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalId: reference(keyVaultManagedIdentityId, '2023-01-31').principalId
  }
}

resource newCertwithRotationKV 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: 'newCertwithRotationKV-${certName}'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${keyVaultManagedIdentityId}': {}
    }
  }
  location: location
  kind: 'AzurePowerShell'
  properties: {
    azPowerShellVersion: '12.0.0'
    arguments: ' -VaultName ${keyVaultName} -ValidityInMonths ${validityInMonths} -IssuerName ${issuerName} -CertName ${certName} -SubjectName ${subjectName} -DnsNames ${join(dnsNames,'_')} -Force ${boolstring}'
    scriptContent: loadTextContent('../../scripts/key-vault-cert.ps1')
    forceUpdateTag: now
    cleanupPreference: 'Always'
    retentionInterval: 'P1D'
    timeout: 'PT5M'
  }
}

output Thumbprint string = newCertwithRotationKV.properties.outputs.Thumbprint
output CACert string = newCertwithRotationKV.properties.outputs.CACert
output KeyVaultCertId string = newCertwithRotationKV.properties.outputs.KeyVaultCertId
