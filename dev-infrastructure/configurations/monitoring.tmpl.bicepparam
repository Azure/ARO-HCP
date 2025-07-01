using '../templates/monitoring.bicep'

param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'

param devAlertingEmails = '{{ .monitoring.devAlertingEmails }}'
param sev1ActionGroupIDs = '{{ .monitoring.sev1ActionGroupIDs }}'
param sev2ActionGroupIDs = '{{ .monitoring.sev2ActionGroupIDs }}'
param sev3ActionGroupIDs = '{{ .monitoring.sev3ActionGroupIDs }}'
param sev4ActionGroupIDs = '{{ .monitoring.sev4ActionGroupIDs }}'
