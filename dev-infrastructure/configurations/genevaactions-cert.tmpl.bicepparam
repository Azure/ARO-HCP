using '../templates/genevaactions-cert.bicep'

param genevaKeyVaultName = '{{ .geneva.action.keyVault.name }}'
param genevaCertificateDomain = '{{ .geneva.action.certificate.domain }}'
param genevaCertificateHostName = '{{ .geneva.action.certificate.hostName }}'
param genevaCertificateIssuer = '{{ .geneva.action.certificate.issuer }}'
param genevaCertificateName = '{{ .geneva.action.certificate.name }}'
param manageGenevaCertificates = {{ .geneva.action.certificate.manage }}
param ev2MsiName = '{{ .global.globalMSIName }}'
