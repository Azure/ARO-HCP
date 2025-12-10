import { safeTake } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('The managed identity name of the logs')
param logsMSI string

@description('The name of the Admin API managed identity')
param adminApiMIName string

// These must match the same vars in modules/metrics/datacollection.bicep
var dceName = safeTake('MSProm-${location}-${aksClusterName}', 44)
var dcrName = safeTake('MSProm-${location}-${aksClusterName}', 44)

resource dce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' existing = {
  name: dceName
}

resource dcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' existing = {
  name: dcrName
}

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'prometheus'
}

resource logsUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: logsMSI
}

resource adminApiUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: adminApiMIName
}

output dcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${dcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
output hcpDcrRemoteWriteUrl string = 'NONE'
output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output clusterLogPrincipalId string = logsUAMI.properties.principalId
output adminApiPrincipalId string = adminApiUAMI.properties.principalId
