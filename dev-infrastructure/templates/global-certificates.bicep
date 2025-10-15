param globalKeyVaultName string
param ev2MsiName string

// genva logs certificate
param genevaLogCertificateDomain string
param genevaLogCertificateHostName string
param genevaLogCertificateIssuer string
param genevaLogsAccountAdmin string
param genevaLogManageCertificates bool

// geneva actions certificate
param genevaActionsKeyVaultName string
param genevaActionsCertificateName string
param genevaActionsCertificateIssuer string
param genevaActionsManageCertificates bool
param genevaActionsCertificateDomain string

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: ev2MsiName
}

// geneva logs certificate

module genevaRPCertificate '../modules/keyvault/key-vault-cert-with-access.bicep' = if (genevaLogManageCertificates) {
  name: 'geveva-logs-account-admin-certificate'
  params: {
    keyVaultName: globalKeyVaultName
    kvCertOfficerManagedIdentityResourceId: ev2MSI.id
    certDomain: genevaLogCertificateDomain
    certificateIssuer: genevaLogCertificateIssuer
    hostName: genevaLogCertificateHostName
    keyVaultCertificateName: genevaLogsAccountAdmin
    certificateAccessManagedIdentityPrincipalId: ev2MSI.properties.principalId
  }
}

// geneva actions certificate

module genevaCertificate '../modules/keyvault/key-vault-cert.bicep' = if (genevaActionsManageCertificates) {
  name: 'geneva-certificate-${uniqueString(resourceGroup().name)}'
  params: {
    keyVaultName: genevaActionsKeyVaultName
    subjectName: 'CN=${genevaActionsCertificateDomain}'
    certName: genevaActionsCertificateName
    keyVaultManagedIdentityId: ev2MSI.id
    dnsNames: [
      genevaActionsCertificateDomain
    ]
    issuerName: genevaActionsCertificateIssuer
  }
}
