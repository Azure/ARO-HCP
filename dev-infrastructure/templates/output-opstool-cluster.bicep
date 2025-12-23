import { safeTake } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('Name for the workload Key Vault')
param workloadKVName string

@description('Azure Monitor Workspace name')
param azureMonitorWorkspaceName string

var dceName = safeTake('MSProm-${location}-${aksClusterName}', 44)
var dcrName = safeTake('MSProm-${location}-${aksClusterName}', 44)

resource dce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' existing = {
  name: dceName
}

resource dcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' existing = {
  name: dcrName
}

resource opstoolUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'opstool'
}

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'prometheus'
}

resource tenantQuotaUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'tenant-quota'
}
resource workloadKV 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: workloadKVName
}

resource azureMonitorWorkspace 'Microsoft.Monitor/accounts@2023-04-03' existing = {
  name: azureMonitorWorkspaceName
}

resource sharedActionGroup 'Microsoft.Insights/actionGroups@2024-10-01-preview' existing = {
  name: 'opstool-email-alerts'
}

output aksClusterName string = aksClusterName
output sharedActionGroupId string = sharedActionGroup.id
output azureMonitorWorkspaceId string = azureMonitorWorkspace.id
output workloadKVName string = workloadKV.name
output opstoolUAMIClientId string = opstoolUAMI.properties.clientId
output opstoolUAMIId string = opstoolUAMI.id
output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output prometheusUAMIId string = prometheusUAMI.id
output tenantQuotaUAMIClientId string = tenantQuotaUAMI.properties.clientId
output tenantQuotaUAMIId string = tenantQuotaUAMI.id
output dcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${dcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'

// Kusto placeholder outputs (opstool doesn't use kusto logging)
output kustoUri string = ''
output kustoDatabase string = ''
output kustoTable string = ''
