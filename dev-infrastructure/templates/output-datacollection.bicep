@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('Are we interested in HCP?')
param outputHcpDcr bool = false

// These must match the same vars in modules/metrics/datacollection.bicep
var dceName = take('MSProm-${location}-${aksClusterName}', 44)
var dcrName = take('MSProm-${location}-${aksClusterName}', 44)
var hcpDcrName = take('HCP-${location}-${aksClusterName}', 44)

resource dce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' existing = {
  name: dceName
}

resource dcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' existing = {
  name: dcrName
}

resource hcpDcr 'Microsoft.Insights/dataCollectionRules@2022-06-01' existing = if (outputHcpDcr) {
  name: hcpDcrName
}

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'prometheus'
}

output dcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${dcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
output hcpDcrRemoteWriteUrl string = outputHcpDcr
  ? '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${hcpDcr!.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
  : ''
output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
