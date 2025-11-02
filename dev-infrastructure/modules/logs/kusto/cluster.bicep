@description('Name of the Kusto cluster to create')
param kustoName string

@description('The SKU of the cluster')
param sku string = 'Standard_D12_v2'

@description('List of cluster principals to create in the Kusto cluster')
param clusterPrincipals array

@description('Name of the Geneva data connection')
param dataConnectionName string

@description('The name of the Geneva Environment.')
param genevaEnvironment string

@description('The MDS account name for rp log')
param rpAccount string

@description('The MDS account name for cluster log')
param clusterAccount string

var accounts = [rpAccount, clusterAccount]

// Core Kusto cluster (no databases here; those are in separate modules)
resource kusto 'Microsoft.Kusto/clusters@2024-04-13' = {
  name: kustoName
  location: resourceGroup().location
  sku: {
    name: sku
    tier: 'Standard'
  }
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    optimizedAutoscale: {
      version: 1
      isEnabled: true
      minimum: 2
      maximum: 10
    }
    enableAutoStop: false
  }

  // Cluster-level permissions
  resource clusterPermissions 'principalAssignments' = [
    for principal in clusterPrincipals: {
      name: principal.name
      properties: {
        principalId: principal.id
        principalType: principal.type
        role: principal.role
        tenantId: principal.tenantId
      }
    }
  ]

  // Geneva data connection
  resource kustoClusterDataConnection 'dataconnections' = {
    name: dataConnectionName
    location: resourceGroup().location
    kind: 'GenevaLegacy'
    properties: {
      genevaEnvironment: genevaEnvironment
      mdsAccounts: accounts
      isScrubbed: true
    }
  }
}

output id string = kusto.id
output uri string = kusto.properties.uri
output principalId string = kusto.identity.principalId
output name string = kusto.name
