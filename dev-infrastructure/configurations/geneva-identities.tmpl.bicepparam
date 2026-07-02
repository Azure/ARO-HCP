using '../templates/geneva-identities.bicep'

param ev2MsiName = '{{ .global.globalMSIName }}'

param globalKeyVaultName = '{{ .global.keyVault.name }}'
param genevaLogCertificateDomain = '{{ .geneva.logs.adminCertificateDomain }}'
param genevaLogCertificateHostName = '{{ .geneva.logs.adminCertName }}'
param genevaLogCertificateIssuer = '{{ .geneva.logs.certificateIssuer }}'
param genevaLogsAccountAdmin = '{{ .geneva.logs.adminCertName }}'
param genevaLogManageCertificates = {{ .geneva.logs.manageCertificates }}

param genevaActionsCertificateDomain = '{{ .geneva.actions.certificate.san }}'
param genevaActionApplicationUseSNI = {{ .geneva.actions.application.useSNI }}
param genevaActionApplicationManage = {{ .geneva.actions.application.manage }}
param genevaActionApplicationName = '{{ .geneva.actions.application.name }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
