using '../templates/global-infra.bicep'

param location = '{{ .global.region }}'
param globalMSIName = '{{ .global.globalMSIName }}'
param cxParentZoneName = '{{ .dns.cxParentZoneName }}'
param svcParentZoneName = '{{ .dns.svcParentZoneName }}'
//  SafeDnsIntApplication object ID use to delegate child DNS
param safeDnsIntAppObjectId = '{{ .global.safeDnsIntAppObjectId }}'

param keyVaultName = '{{ .global.keyVault.name}}'
param keyVaultPrivate = {{ .global.keyVault.private }}
param keyVaultSoftDelete = {{ .global.keyVault.softDelete }}
param keyVaultTagKey = '{{ .global.keyVault.tagKey }}'
param keyVaultTagValue = '{{ .global.keyVault.tagValue }}'

param grafanaName = '{{ .monitoring.grafanaName }}'
param grafanaMajorVersion = '{{ .monitoring.grafanaMajorVersion }}'
param grafanaZoneRedundantMode = '{{ .monitoring.grafanaZoneRedundantMode }}'
param grafanaRoles = '{{ .monitoring.grafanaRoles }}'

param globalNSPName = '{{ .global.nsp.name }}'
param globalNSPAccessMode = '{{ .global.nsp.accessMode }}'

// Azure Front Door
param oidcSubdomain = '{{ .oidc.frontdoor.subdomain }}'
param azureFrontDoorProfileName = '{{ .oidc.frontdoor.name }}'
param azureFrontDoorSkuName = '{{ .oidc.frontdoor.sku }}'
param azureFrontDoorKeyVaultName = '{{ .oidc.frontdoor.keyVault.name }}'
param azureFrontDoorKeyVaultTagKey = '{{ .oidc.frontdoor.keyVault.tagKey }}'
param azureFrontDoorKeyVaultTagValue = '{{ .oidc.frontdoor.keyVault.tagValue }}'
param azureFrontDoorUseManagedCertificates = {{ .oidc.frontdoor.useManagedCertificates }}
param keyVaultAdminPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
param oidcMsiName = '{{ .oidc.frontdoor.msiName }}'
