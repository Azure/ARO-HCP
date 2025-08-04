using '../templates/global-certificates.bicep'

param globalKeyVaultName = '{{ .global.keyVault.name }}'
param genevaCertificateDomain = '{{ .geneva.logs.adminCertificateDomain }}'
param genevaCertificateHostName = '{{ .geneva.logs.adminCertName }}'
param genevaCertificateIssuer = '{{ .geneva.logs.certificateIssuer }}'
param genevaLogsAccountAdmin = '{{ .geneva.logs.adminCertName }}'
param genevaManageCertificates = {{ .geneva.logs.manageCertificates }}
param ev2MsiName = '{{ .global.globalMSIName }}'
