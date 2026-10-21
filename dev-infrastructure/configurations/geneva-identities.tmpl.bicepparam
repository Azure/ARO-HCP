using '../templates/geneva-identities.bicep'

param genevaActionsCertificateDomain = '{{ .geneva.actions.certificate.san }}'
param genevaActionApplicationUseSNI = {{ .geneva.actions.application.useSNI }}
param genevaActionApplicationManage = {{ .geneva.actions.application.manage }}
param genevaActionApplicationName = '{{ .geneva.actions.application.name }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
