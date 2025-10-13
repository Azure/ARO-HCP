using '../templates/genevaactions-cert.bicep'

param genevaKeyVaultName = '{{ .geneva.keyVault.name }}'
param genevaCertificateDomain = '{{ .geneva.certificate.domain }}'
param genevaCertificateHostName = '{{ .geneva.certificate.hostName }}'
param genevaCertificateIssuer = '{{ .geneva.certificate.issuer }}'
param genevaCertificateName = '{{ .geneva.certificate.name }}'
param manageGenevaCertificates = {{ .geneva.certificate.manage }}
param ev2MsiName = '{{ .global.globalMSIName }}'
