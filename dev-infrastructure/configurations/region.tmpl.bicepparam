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
param svcAmwMaxActiveTimeSeries = {{ if eq (printf "%T" .monitoring.svcMaxActiveTimeSeries) "float64" }}{{ printf "%.0f" .monitoring.svcMaxActiveTimeSeries }}{{ else }}{{ .monitoring.svcMaxActiveTimeSeries }}{{ end }}
param svcAmwMaxEventsPerMinute = {{ if eq (printf "%T" .monitoring.svcMaxEventsPerMinute) "float64" }}{{ printf "%.0f" .monitoring.svcMaxEventsPerMinute }}{{ else }}{{ .monitoring.svcMaxEventsPerMinute }}{{ end }}
param hcpAmwMaxActiveTimeSeries = {{ if eq (printf "%T" .monitoring.hcpMaxActiveTimeSeries) "float64" }}{{ printf "%.0f" .monitoring.hcpMaxActiveTimeSeries }}{{ else }}{{ .monitoring.hcpMaxActiveTimeSeries }}{{ end }}
param hcpAmwMaxEventsPerMinute = {{ if eq (printf "%T" .monitoring.hcpMaxEventsPerMinute) "float64" }}{{ printf "%.0f" .monitoring.hcpMaxEventsPerMinute }}{{ else }}{{ .monitoring.hcpMaxEventsPerMinute }}{{ end }}
