param genevaKeyVaultName string
param genevaCertificateDomain string
param genevaCertificateHostName string
param genevaCertificateIssuer string
param genevaCertificateName string
param manageGenevaCertificates bool
param ev2MsiName string

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: ev2MsiName
}

module genevaCertificate '../modules/keyvault/key-vault-cert-with-access.bicep' = if (manageGenevaCertificates) {
  name: 'geneva-certificate-${uniqueString(resourceGroup().name)}'
  params: {
    keyVaultName: genevaKeyVaultName
    kvCertOfficerManagedIdentityResourceId: ev2MSI.id
    certDomain: genevaCertificateDomain
    certificateIssuer: genevaCertificateIssuer
    hostName: genevaCertificateHostName
    keyVaultCertificateName: genevaCertificateName
    certificateAccessManagedIdentityPrincipalId: ev2MSI.properties.principalId
  }
}
