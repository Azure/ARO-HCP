import { safeTake } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('The managed identity name of the logs')
param logsMSI string

param svcWorkspaceLocation string
param hcpWorkspaceLocation string

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

// Decide from pipeline inputs before ARM evaluates references to the optional HCP DCE.
resource hcpDce 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' existing = if (hcpWorkspaceLocation != svcWorkspaceLocation) {
  name: hcpDcrName
}

var hcpIngestionEndpoint = hcpWorkspaceLocation != svcWorkspaceLocation
  ? hcpDce!.properties.metricsIngestion.endpoint
  : dce.properties.metricsIngestion.endpoint

output dcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${dcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
output hcpDcrRemoteWriteUrl string = '${hcpIngestionEndpoint}/dataCollectionRules/${hcpDcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
output azureMonitoringWorkspaceId string = dcr.properties.destinations.monitoringAccounts[0].accountResourceId
output azureMonitorWorkspaceLocation string = dcr.location
output hcpAzureMonitoringWorkspaceId string = hcpDcr.properties.destinations.monitoringAccounts[0].accountResourceId
output hcpAzureMonitorWorkspaceLocation string = hcpDcr.location
output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output clusterLogPrincipalId string = logsUAMI.properties.principalId
