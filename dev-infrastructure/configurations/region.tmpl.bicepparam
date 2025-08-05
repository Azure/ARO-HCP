using '../templates/region.bicep'

// dns
param cxParentZoneResourceId = '__cxParentZoneResourceId__'
param svcParentZoneResourceId = '__svcParentZoneResourceId__'
param regionalDNSSubdomain = '{{ .dns.regionalSubdomain }}'

// maestro
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroEventGridMaxClientSessionsPerAuthName = {{ .maestro.eventGrid.maxClientSessionsPerAuthName }}
param maestroEventGridPrivate = {{ .maestro.eventGrid.private }}
param maestroCertificateIssuer = '{{ .maestro.certIssuer }}'

// MI for resource access during pipeline runs
param globalMSIId = '__globalMSIId__'

// Log Analytics
param enableLogAnalytics = {{ .logs.loganalytics.enable }}

// Monitoring
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
param grafanaResourceId = '__grafanaResourceId__'
