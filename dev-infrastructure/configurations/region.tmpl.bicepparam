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

// Monitoring
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
param grafanaResourceId = '__grafanaResourceId__'
param amwMaxActiveTimeSeries = {{ if eq (printf "%T" .monitoring.maxActiveTimeSeries) "float64" }}{{ printf "%.0f" .monitoring.maxActiveTimeSeries }}{{ else }}{{ .monitoring.maxActiveTimeSeries }}{{ end }}
param amwMaxEventsPerMinute = {{ if eq (printf "%T" .monitoring.maxEventsPerMinute) "float64" }}{{ printf "%.0f" .monitoring.maxEventsPerMinute }}{{ else }}{{ .monitoring.maxEventsPerMinute }}{{ end }}
