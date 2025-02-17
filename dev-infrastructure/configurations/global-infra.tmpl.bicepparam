using '../templates/global-infra.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'

// DNS
param cxParentZoneName = '{{ .dns.cxParentZoneName }}'
param svcParentZoneName = '{{ .dns.svcParentZoneName }}'

// Grafana
param grafanaName = '{{ .monitoring.grafanaName }}'
param grafanaAdminGroupPrincipalId = '{{ .monitoring.grafanaAdminGroupPrincipalId }}'
//  SafeDnsIntApplication object ID use to delegate child DNS
param safeDnsIntAppObjectId = '{{ .global.safeDnsIntAppObjectId }}'

// Azure Front Door
param oidcSubdomain = '{{ .oidc.subdomain }}'
param azureFrontDoorProfileName = '{{ .oidc.frontdoor.name }}'
param azureCloudName = '{{ .azure.cloudName }}'
param azureFrontDoorkeyVaultName = '{{ .oidc.frontdoor.keyVaultName }}'
param keyVaultAdminPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
param oidcMsiName = '{{ .oidc.frontdoor.msiName }}'
