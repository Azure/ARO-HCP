param globalKeyVaultName string
param genevaCertificateDomain string
param genevaCertificateHostName string
param genevaCertificateIssuer string
param genevaLogsAccountAdmin string
param genevaManageCertificates bool
param ev2MsiName string

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: ev2MsiName
}

module genevaRPCertificate '../modules/keyvault/key-vault-cert-with-access.bicep' = if (genevaManageCertificates) {
  name: 'geveva-logs-account-admin-certificate'
  params: {
    keyVaultName: globalKeyVaultName
    kvCertOfficerManagedIdentityResourceId: ev2MSI.id
    certDomain: genevaCertificateDomain
    certificateIssuer: genevaCertificateIssuer
    hostName: genevaCertificateHostName
    keyVaultCertificateName: genevaLogsAccountAdmin
    certificateAccessManagedIdentityPrincipalId: ev2MSI.properties.principalId
  }
}
