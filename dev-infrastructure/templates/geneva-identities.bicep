import {
  csvToArray
} from '../modules/common.bicep'

param globalKeyVaultName string
param ev2MsiName string

@description('The name of the storage account for deployment scripts')
param deploymentScriptStorageAccountName string

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
param entraAppOwnerIds string
param genevaActionApplicationManage bool
param genevaActionApplicationUseSNI bool

resource ev2MSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: ev2MsiName
}

//
//   D E P L O Y M E N T   S C R I P T   S T O R A G E
//

module deploymentScriptStorage '../modules/deployment-script-storage.bicep' = {
  name: 'deployment-script-storage'
  params: {
    storageAccountName: deploymentScriptStorageAccountName
    location: resourceGroup().location
    managedIdentityPrincipalIds: [
      ev2MSI.properties.principalId
    ]
  }
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
    deploymentScriptStorageAccountName: deploymentScriptStorage.outputs.storageAccountName
    deploymentScriptSubnetId: deploymentScriptStorage.outputs.subnetId
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
    deploymentScriptStorageAccountName: deploymentScriptStorage.outputs.storageAccountName
    deploymentScriptSubnetId: deploymentScriptStorage.outputs.subnetId
  }
}

output PublicKey string = genevaCertificate.outputs.PublicKey

// //   G E N E V A    A C T I O N S   A P P   R E G I S T R A T I O N

var genevaKeyCredentials = !genevaActionApplicationUseSNI
  ? [
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
    ]
  : []

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
    keyCredentials: genevaKeyCredentials
  }
}
