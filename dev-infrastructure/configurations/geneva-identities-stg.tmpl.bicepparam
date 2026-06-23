// TRANSIENT: STG-global "V2" copy of geneva-identities.tmpl.bicepparam. Identical
// to the canonical file except the global and Geneva Key Vault names are sourced
// from the transient stgGlobalV2 block. Removed at decommission.
using '../templates/geneva-identities.bicep'

param ev2MsiName = '{{ .global.globalMSIName }}'

param globalKeyVaultName = '{{ .stgGlobalV2.globalKeyVaultName }}'
param genevaLogCertificateDomain = '{{ .geneva.logs.adminCertificateDomain }}'
param genevaLogCertificateHostName = '{{ .geneva.logs.adminCertName }}'
param genevaLogCertificateIssuer = '{{ .geneva.logs.certificateIssuer }}'
param genevaLogsAccountAdmin = '{{ .geneva.logs.adminCertName }}'
param genevaLogManageCertificates = {{ .geneva.logs.manageCertificates }}

param genevaActionsKeyVaultName = '{{ .stgGlobalV2.genevaKeyVaultName }}'
param genevaActionsCertificateDomain = '{{ .geneva.actions.certificate.name }}.{{ .dns.globalCertificatesDomain }}'
param genevaActionsCertificateIssuer = '{{ .geneva.actions.certificate.issuer }}'
param genevaActionsCertificateName = '{{ .geneva.actions.certificate.name }}'
param genevaActionsManageCertificates = {{ .geneva.actions.certificate.manage }}
param genevaActionApplicationUseSNI = {{ .geneva.actions.application.useSNI }}
param genevaActionApplicationManage = {{ .geneva.actions.application.manage }}
param genevaActionApplicationName = '{{ .geneva.actions.application.name }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
