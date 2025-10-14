using '../templates/genevaactions-cert.bicep'

param genevaKeyVaultName = '{{ .geneva.actions.keyVault.name }}'
param svcDNSZoneName = '{{ .dns.svcParentZoneName }}'
param genevaCertificateIssuer = '{{ .geneva.actions.certificate.issuer }}'
param genevaCertificateName = '{{ .geneva.actions.certificate.name }}'
param manageGenevaCertificates = {{ .geneva.actions.certificate.manage }}
param ev2MsiName = '{{ .global.globalMSIName }}'
