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
param crossTenantSecurityGroup = '{{ .monitoring.crossTenantSecurityGroup }}'

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
param azureFrontDoorManage = {{ .oidc.frontdoor.manage }}

// SP for KV certificate issuer registration
param kvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'

// SP for EV2 certificate access, i.e. geneva log access
param kvCertAccessPrincipalId = '{{ .kvCertAccessPrincipalId }}'
param kvCertAccessRoleId = '{{ .kvCertAccessRoleId }}'

// Geneva Actions
param genevaKeyVaultName = '{{ .geneva.actions.keyVault.name}}'
param genevaKeyVaultPrivate = {{ .geneva.actions.keyVault.private }}
param genevaKeyVaultSoftDelete = {{ .geneva.actions.keyVault.softDelete }}
param genevaKeyVaultTagKey = '{{ .geneva.actions.keyVault.tagKey }}'
param genevaKeyVaultTagValue = '{{ .geneva.actions.keyVault.tagValue }}'

param allowedAcisExtensions = '{{ .geneva.actions.allowedAcisExtensions }}'
param genevaActionsPrincipalId = '{{ .geneva.actions.genevaActionsPrincipalId }}'
