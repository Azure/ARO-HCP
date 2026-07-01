// TRANSIENT: STG-global "V2" copy of global-infra.tmpl.bicepparam. Identical to the
// canonical file except the globally-unique resource names and DNS parent zones are
// sourced from the transient stgGlobalV2 block so the parallel STG-global stack does
// not collide with the live (shared-subscription) names/zones. Removed at decommission.
using '../templates/global-infra.bicep'

param location = '{{ .global.region }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param cxParentZoneName = '{{ .stgGlobalV2.cxParentZoneName }}'
param svcParentZoneName = '{{ .stgGlobalV2.svcParentZoneName }}'
//  SafeDnsIntApplication object ID use to delegate child DNS
param safeDnsIntAppObjectId = '{{ .global.safeDnsIntAppObjectId }}'

param keyVaultName = '{{ .stgGlobalV2.globalKeyVaultName }}'
param keyVaultPrivate = {{ .global.keyVault.private }}
param keyVaultSoftDelete = {{ .global.keyVault.softDelete }}
param keyVaultTagKey = '{{ .global.keyVault.tagKey }}'
param keyVaultTagValue = '{{ .global.keyVault.tagValue }}'
param keyVaultEncryptionKeyName = '{{ .global.keyVault.encryptionKeyName }}'

// V2 grafana name from stgGlobalV2 (not monitoring.grafanaName) so the parallel
// STG-global stack does not collide with the live arohcp-stg Grafana workspace.
param grafanaName = '{{ .stgGlobalV2.grafanaName }}'
// V2 grafana major version from stgGlobalV2: the new workspace must be created on
// v13 (Azure retired v11 for new creation 2026-06-15); the live fleet stays on 11.
param grafanaMajorVersion = '{{ .stgGlobalV2.grafanaMajorVersion }}'
param grafanaZoneRedundantMode = '{{ .monitoring.grafanaZoneRedundantMode }}'
param grafanaRoles = '{{ .monitoring.grafanaRoles }}'
param crossTenantSecurityGroup = '{{ .monitoring.crossTenantSecurityGroup }}'

param globalNSPName = '{{ .global.nsp.name }}'
param globalNSPAccessMode = '{{ .global.nsp.accessMode }}'

// Azure Front Door
param oidcSubdomain = '{{ .oidc.frontdoor.subdomain }}'
param azureFrontDoorProfileName = '{{ .stgGlobalV2.frontDoorName }}'
param azureFrontDoorSkuName = '{{ .oidc.frontdoor.sku }}'
param azureFrontDoorKeyVaultName = '{{ .stgGlobalV2.frontDoorKeyVaultName }}'
param azureFrontDoorKeyVaultTagKey = '{{ .oidc.frontdoor.keyVault.tagKey }}'
param azureFrontDoorKeyVaultTagValue = '{{ .oidc.frontdoor.keyVault.tagValue }}'
param azureFrontDoorUseManagedCertificates = {{ .oidc.frontdoor.useManagedCertificates }}
param keyVaultAdminPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
param oidcMsiName = '{{ .oidc.frontdoor.msiName }}'
param azureFrontDoorManage = {{ .oidc.frontdoor.manage }}

// SP for KV certificate issuer registration
param kvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'

// SP for EV2 certificate access, i.e. geneva log access
param kvCertAccessPrincipalId = '{{ .kvCertAccessPrincipalId }}'
param kvCertAccessRoleId = '{{ .kvCertAccessRoleId }}'

// Geneva Actions
param genevaKeyVaultName = '{{ .stgGlobalV2.genevaKeyVaultName }}'
param genevaKeyVaultPrivate = {{ .geneva.actions.keyVault.private }}
param genevaKeyVaultSoftDelete = {{ .geneva.actions.keyVault.softDelete }}
param genevaKeyVaultTagKey = '{{ .geneva.actions.keyVault.tagKey }}'
param genevaKeyVaultTagValue = '{{ .geneva.actions.keyVault.tagValue }}'

param allowedAcisExtensions = '{{ .geneva.actions.allowedAcisExtensions }}'
param genevaActionsPrincipalId = '{{ .geneva.actions.genevaActionsPrincipalId }}'
