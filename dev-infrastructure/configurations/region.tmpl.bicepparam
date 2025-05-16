using '../templates/region.bicep'

// general
param globalRegion = '{{ .global.region }}'
param regionalRegion = '{{ .region }}'

// acr
param ocpAcrResourceId = '__ocpAcrResourceId__'
param svcAcrResourceId = '__svcAcrResourceId__'

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
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

// Log Analytics
param enableLogAnalytics = {{ .logs.loganalytics.enable }}

// Metrics
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
param grafanaResourceId = '__grafanaResourceId__'

param devAlertingEmails = '{{ .monitoring.devAlertingEmails }}'
param sev1ActionGroupIDs = '{{ .monitoring.sev1ActionGroupIDs }}'
param sev2ActionGroupIDs = '{{ .monitoring.sev2ActionGroupIDs }}'
param sev3ActionGroupIDs = '{{ .monitoring.sev3ActionGroupIDs }}'
param sev4ActionGroupIDs = '{{ .monitoring.sev4ActionGroupIDs }}'
