using '../templates/global-infra.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'
param cxParentZoneName = '{{ .global.cxParentZoneName }}'
param svcParentZoneName = '{{ .global.svcParentZoneName }}'
