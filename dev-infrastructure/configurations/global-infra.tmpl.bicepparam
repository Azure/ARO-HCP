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

param genevaKeyVaultName = '{{ .geneva.keyVault.name}}'
param genevaKeyVaultPrivate = {{ .geneva.keyVault.private }}
param genevaKeyVaultSoftDelete = {{ .geneva.keyVault.softDelete }}

param genevaCertificateName = '{{ .geneva.genevaActionCertificate.name }}'
param genevaCertificateIssuer = '{{ .geneva.genevaActionCertificate.issuer }}'
param genevaCertificateManage = {{ .geneva.genevaActionCertificate.manage }}

param svcDNSZoneName = '{{ .dns.svcParentZoneName }}'

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
param azureFrontDoorkeyVaultName = '{{ .oidc.frontdoor.keyVaultName }}'
param keyVaultAdminPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
param oidcMsiName = '{{ .oidc.frontdoor.msiName }}'
