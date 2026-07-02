import {
  csvToArray
} from '../modules/common.bicep'

param globalKeyVaultName string
param ev2MsiName string

// genva logs certificate
param genevaLogCertificateDomain string
param genevaLogCertificateHostName string
param genevaLogCertificateIssuer string
param genevaLogsAccountAdmin string
param genevaLogManageCertificates bool

// geneva actions certificate
param genevaActionsCertificateDomain string
param genevaActionApplicationName string
param entraAppOwnerIds string
param genevaActionApplicationManage bool
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

// //   G E N E V A    A C T I O N S   A P P   R E G I S T R A T I O N
//
// When useSNI is false (dev), the app has no auth mechanism configured here.
// The certificate is managed by a CreateCertificate pipeline step, but keyCredentials
// are not wired up. If dev ever needs to authenticate to the app, the certificate
// must be attached to the app registration through another mechanism.

module entraApp '../modules/entra/app.bicep' = if (genevaActionApplicationManage) {
  name: 'geneva-actions-entra-app'
  params: {
    applicationName: genevaActionApplicationName
    ownerIds: entraAppOwnerIds
    isFallbackPublicClient: true
    manageSp: true
    trustedSubjectNameAndIssuers: genevaActionApplicationUseSNI
      ? [
          {
            authorityId: '00000000-0000-0000-0000-000000000001'
            subjectName: genevaActionsCertificateDomain
          }
        ]
      : []
    serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
  }
}
