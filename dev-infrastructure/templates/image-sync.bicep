@description('Azure Region Location')
param location string = resourceGroup().location

@description('Specifies the name of the container app environment.')
param containerAppEnvName string = 'image-sync-env-${uniqueString(resourceGroup().id)}'

@description('Specifies the name of the log analytics workspace.')
param containerAppLogAnalyticsName string = 'containerapp-log-${uniqueString(resourceGroup().id)}'

resource logAnalytics 'Microsoft.OperationalInsights/workspaces@2021-06-01' = {
  name: containerAppLogAnalyticsName
  location: location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
  }
}

resource containerAppEnvironment 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: containerAppEnvName
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logAnalytics.properties.customerId
        sharedKey: logAnalytics.listKeys().primarySharedKey
      }
    }
  }
}
