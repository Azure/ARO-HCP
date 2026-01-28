import { safeTake } from '../modules/common.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

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

output aksClusterName string = aksClusterName
output opstoolUAMIClientId string = opstoolUAMI.properties.clientId
output opstoolUAMIId string = opstoolUAMI.id
output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output prometheusUAMIId string = prometheusUAMI.id
output dcrRemoteWriteUrl string = '${dce.properties.metricsIngestion.endpoint}/dataCollectionRules/${dcr.properties.immutableId}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24'
