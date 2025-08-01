using '../templates/global-certificates.bicep'

param globalKeyVaultName = '{{ .global.keyVault.name }}'
param genevaCertificateDomain = '{{ .logs.certificateDomain }}'
param genevaCertificateHostName = '{{ .logs.adminCertDomain }}'
param genevaCertificateIssuer = '{{ .logs.certificateIssuer }}'
param genevaLogsAccountAdmin = '{{ .logs.adminCertName }}'
param genevaManageCertificates = {{ .logs.manageCertificates }}
param ev2MsiName = '{{ .global.globalMSIName }}'
