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
param genevaActionApplicationName string
param genevaActionApplicationOwnerId string
param genevaActionApplicationCreation bool
param genevaActionApplicationUseSNI bool

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: ev2MsiName
}

//   G E N E V A    L O G S   C E R T I F I C A T E

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

//   G E N E V A    A C T I O N S   C E R T I F I C A T E

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

output PublicKey string = genevaCertificate.outputs.PublicKey

// //   G E N E V A    A C T I O N S   A P P   R E G I S T R A T I O N

extension microsoftGraphBeta

resource genevaApp 'Microsoft.Graph/applications@beta' = if (genevaActionApplicationCreation) {
  displayName: genevaActionApplicationName
  isFallbackPublicClient: true
  signInAudience: 'AzureADMyOrg' // Single tenant applicaion
  uniqueName: genevaActionApplicationName
  requiredResourceAccess: []
  serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
  trustedSubjectNameAndIssuers: genevaActionApplicationUseSNI ? [
    {
      authorityId: '00000000-0000-0000-0000-000000000001'
      subjectName: genevaActionsCertificateDomain
    }
  ] : []
  owners: {
    relationships: [
      genevaActionApplicationOwnerId
    ]
  }
  keyCredentials: !genevaActionApplicationUseSNI ? [
    {
      type: 'AsymmetricX509Cert'
      usage: 'Verify'
      displayName: 'Geneva Action Login - ${genevaCertificate.outputs.Thumbprint}'
      key: genevaCertificate.outputs.PublicKey
      keyId: guid(genevaCertificate.outputs.Thumbprint)
      customKeyIdentifier: genevaCertificate.outputs.KeyIdentifier
      startDateTime: genevaCertificate.outputs.NotBefore
      endDateTime: genevaCertificate.outputs.NotAfter
    }
  ] : []
}

resource genevaSp 'Microsoft.Graph/servicePrincipals@beta' = if (genevaActionApplicationCreation) {
  appId: genevaApp.appId
}
