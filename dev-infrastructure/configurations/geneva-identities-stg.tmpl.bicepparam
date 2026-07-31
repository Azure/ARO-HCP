// TRANSIENT: STG-global "V2" copy of geneva-identities.tmpl.bicepparam. Identical
// to the canonical file except the global and Geneva Key Vault names are sourced
// from the transient stgGlobalV2 block. Removed at decommission.
using '../templates/geneva-identities.bicep'

param genevaActionsCertificateDomain = '{{ .geneva.actions.certificate.san }}'
param genevaActionApplicationUseSNI = {{ .geneva.actions.application.useSNI }}
// Coexistence: the Geneva Actions Entra app is environment-scoped (arohcp-ga-stg)
// and is managed by both the canonical stg global rollout and this transient V2
// rollout. Both set the same fixed entraAppOwnerIds, so app ownership races between
// them: whichever ran last owns it, and the other rollout's deploy identity (which
// only holds Application.ReadWrite.OwnedBy) then fails to manage it with
// Authorization_RequestDenied. This regressed the V2 rollout after a run where it
// had briefly owned the app. The V2 global rollout does not consume this app, so
// skip managing it here to end the tug-of-war. Revisit at decommission.
param genevaActionApplicationManage = false
param genevaActionApplicationName = '{{ .geneva.actions.application.name }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
