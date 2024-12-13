using '../templates/global-infra.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'
param cxParentZoneName = '{{ .baseDnsZoneName }}'
param svcParentZoneName = '{{ .svcParentZoneName }}'
