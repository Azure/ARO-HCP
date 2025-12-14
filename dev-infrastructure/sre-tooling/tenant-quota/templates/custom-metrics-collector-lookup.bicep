@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

resource customMetricsCollectorUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'custom-metrics-collector'
}

output customMetricsCollectorUAMIClientId string = customMetricsCollectorUAMI.properties.clientId

