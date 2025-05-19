using '../modules/metrics/metrics.bicep'

param monitorName = '{{ .monitoring.workspaceName }}'
param grafanaResourceId = '__grafanaResourceId__'

param devAlertingEmails = '{{ .monitoring.devAlertingEmails }}'
param sev1ActionGroupIDs = '{{ .monitoring.sev1ActionGroupIDs }}'
param sev2ActionGroupIDs = '{{ .monitoring.sev2ActionGroupIDs }}'
param sev3ActionGroupIDs = '{{ .monitoring.sev3ActionGroupIDs }}'
param sev4ActionGroupIDs = '{{ .monitoring.sev4ActionGroupIDs }}'
