@description('Name of the Log Analytics workspace')
param workspaceName string

@description('Azure region for the workspace')
param location string

@description('SKU for the Log Analytics workspace')
param sku string = 'PerGB2018'

@description('Data retention in days')
param retentionInDays int = 90

resource workspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: workspaceName
  location: location
  properties: {
    sku: {
      name: sku
    }
    retentionInDays: retentionInDays
  }
}

output workspaceId string = workspace.id
