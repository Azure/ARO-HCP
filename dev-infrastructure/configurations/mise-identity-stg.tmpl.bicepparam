// TRANSIENT: STG-global "V2" copy of mise-identity.tmpl.bicepparam. Removed at decommission.
//
// Coexistence: the MISE Entra app is environment-scoped (arohcp-mise-stg) and is
// managed by both the canonical stg global rollout and this transient V2 rollout.
// Both set the same fixed entraAppOwnerIds, so app ownership races between them:
// whichever ran last owns it, and the other rollout's deploy identity (which only
// holds Application.ReadWrite.OwnedBy) then fails to manage it with
// Authorization_RequestDenied. The V2 global rollout does not consume this app
// (the stg svc rollout looks it up independently), so skip deploying it here to end
// the tug-of-war. Revisit at decommission.
using '../templates/mise-identity.bicep'

param miseApplicationName = '{{ .mise.applicationName }}'
param entraAppOwnerIds = '{{ .entraAppOwnerIds }}'
param miseApplicationDeploy = false
