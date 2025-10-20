using '../templates/global-certificates.bicep'

param ev2MsiName = '{{ .global.globalMSIName }}'

param globalKeyVaultName = '{{ .global.keyVault.name }}'
param genevaLogCertificateDomain = '{{ .geneva.logs.adminCertificateDomain }}'
param genevaLogCertificateHostName = '{{ .geneva.logs.adminCertName }}'
param genevaLogCertificateIssuer = '{{ .geneva.logs.certificateIssuer }}'
param genevaLogsAccountAdmin = '{{ .geneva.logs.adminCertName }}'
param genevaLogManageCertificates = {{ .geneva.logs.manageCertificates }}

param genevaActionsKeyVaultName = '{{ .geneva.actions.keyVault.name }}'
param genevaActionsCertificateDomain = '{{ .geneva.actions.certificate.name }}.{{ .dns.globalCertificatesDomain }}'
param genevaActionsCertificateIssuer = '{{ .geneva.actions.certificate.issuer }}'
param genevaActionsCertificateName = '{{ .geneva.actions.certificate.name }}'
param genevaActionsManageCertificates = {{ .geneva.actions.certificate.manage }}
param genevaActionApplicationUseSNI = false
param genevaActionApplicationCreation = true
param genevaActionApplicationName = '{{ .geneva.actions.applicationName }}'
param genevaActionApplicationOwnerId = '{{ .geneva.actions.applicationOwnerId }}'
