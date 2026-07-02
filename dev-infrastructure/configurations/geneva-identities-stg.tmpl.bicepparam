// TRANSIENT: STG-global "V2" copy of geneva-identities.tmpl.bicepparam. Identical
// to the canonical file except the global and Geneva Key Vault names are sourced
// from the transient stgGlobalV2 block. Removed at decommission.
using '../templates/geneva-identities.bicep'

param genevaActionsCertificateDomain = '{{ .geneva.actions.certificate.san }}'
param genevaActionApplicationUseSNI = {{ .geneva.actions.application.useSNI }}
param genevaActionApplicationManage = {{ .geneva.actions.application.manage }}
param genevaActionApplicationName = '{{ .geneva.actions.application.name }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
