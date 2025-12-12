import { safeTake } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('The managed identity name of the logs')
param logsMSI string

// These must match the same vars in modules/metrics/datacollection.bicep
var dceName = safeTake('MSProm-${location}-${aksClusterName}', 44)
var dcrName = safeTake('MSProm-${location}-${aksClusterName}', 44)
var hcpDcrName = safeTake('HCP-${location}-${aksClusterName}', 44)

resource dce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' existing = {
  name: dceName
}

resource dcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' existing = {
  name: dcrName
}

resource hcpDcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' existing = {
  name: hcpDcrName
}

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'prometheus'
}

resource logsUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: logsMSI
}

resource customMetricsCollectorUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'custom-metrics-collector'
}

output dcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${dcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
output hcpDcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${hcpDcr!.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output clusterLogPrincipalId string = logsUAMI.properties.principalId
output customMetricsCollectorUAMIClientId string = customMetricsCollectorUAMI.properties.clientId
