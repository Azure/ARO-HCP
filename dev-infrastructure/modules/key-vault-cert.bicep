/*
Creating certificates in Azure Key Vault is not supported by Bicep yet.
This module leverages a deploymentscript to solve this for the time beeing.
Proudly stolen from https://github.com/Azure/bicep/discussions/8457

We might not need certificates for MQTT authentication altogether if
Entra autentication can be leveraged: https://redhat-external.slack.com/archives/C03F6AA3HDH/p1713340078776669
*/

param keyVaultName string
param certName string
param subjectName string
param issuerName string
param dnsNames array
param now string = utcNow('F')
param keyVaultManagedIdentityId string
param location string
param force bool = false
var boolstring = force == false ? '$false' : '$true'

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
    azPowerShellVersion: '7.5.0'
    arguments: ' -VaultName ${keyVaultName} -IssuerName ${issuerName} -CertName ${certName} -SubjectName ${subjectName} -DnsNames ${join(dnsNames,'_')} -Force ${boolstring}'
    scriptContent: loadTextContent('../scripts/key-vault-cert.ps1')
    forceUpdateTag: now
    cleanupPreference: 'Always'
    retentionInterval: 'P1D'
    timeout: 'PT5M'
  }
}

output Thumbprint string = newCertwithRotationKV.properties.outputs.Thumbprint
output CACert string = newCertwithRotationKV.properties.outputs.CACert
output KeyVaultCertId string = newCertwithRotationKV.properties.outputs.KeyVaultCertId
