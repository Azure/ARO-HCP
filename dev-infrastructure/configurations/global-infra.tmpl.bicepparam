using '../templates/global-infra.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'
param cxParentZoneName = '{{ .dns.cxParentZoneName }}'
param svcParentZoneName = '{{ .dns.svcParentZoneName }}'
param grafanaName = '{{ .monitoring.grafanaName }}'
param msiName = '{{ .monitoring.msiName }}'
param grafanaAdminGroupPrincipalId = '{{ .monitoring.grafanaAdminGroupPrincipalId }}'
//  SafeDnsIntApplication object ID use to delegate child DNS
param safeDnsIntAppObjectId = '{{ .global.safeDnsIntAppObjectId }}'
