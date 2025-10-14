param genevaKeyVaultName string
param svcDNSZoneName string
param genevaCertificateIssuer string
param genevaCertificateName string
param manageGenevaCertificates bool
param ev2MsiName string

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: ev2MsiName
}

var genevaCertificateSNI = '${genevaCertificateName}.${svcDNSZoneName}'

module genevaCertificate '../modules/keyvault/key-vault-cert.bicep' = if (manageGenevaCertificates) {
  name: 'geneva-certificate-${uniqueString(resourceGroup().name)}'
  params: {
    keyVaultName: genevaKeyVaultName
    subjectName: 'CN=${genevaCertificateSNI}'
    certName: genevaCertificateName
    keyVaultManagedIdentityId: ev2MSI.id
    dnsNames: [
      genevaCertificateSNI
    ]
    issuerName: genevaCertificateIssuer
  }
}
