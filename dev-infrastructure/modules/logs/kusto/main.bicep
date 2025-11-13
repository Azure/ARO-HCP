import { getGeoShortForRegion } from '../../common.bicep'

@description('The SKU of the cluster')
param sku string = 'Standard_D12_v2'

@description('Tier used')
param tier string = 'Basic'

@description('List of dSTS groups')
param dstsGroups array

@description('Azure Region Location')
param location string

@description('Name of the service logs database.')
param serviceLogsDatabase string

@description('Name of the customer logs database.')
param customerLogsDatabase string

@description('CSV seperated list of groups to assign admin in the Kusto cluster')
param adminGroups string

@description('CSV seperated list of groups to assign viewer in the Kusto cluster')
param viewerGroups string

var kustoName = 'hcp-${getGeoShortForRegion(location)}'

var db = {
  serviceLogs: serviceLogsDatabase
  customerLogs: customerLogsDatabase
}

var databases = [db.serviceLogs, db.customerLogs]

var dummyScript = '.create-or-alter function with (docstring = \'dummy function to run last and to remove permission\') dummyFunction() {print \'dummy\'}'

var allServiceLogsTablesKQL = {
  backendContainerLogs: loadTextContent('tables/backendContainerLogs.kql')
  containerlogs: loadTextContent('tables/containerLogs.kql')
  frontendContainerLogs: loadTextContent('tables/frontendContainerLogs.kql')
}

var allCustomerLogsTablesKQL = {
  containerlogs: loadTextContent('tables/containerLogs.kql')
}

// 1. Cluster
module cluster 'cluster.bicep' = {
  name: 'cluster'
  params: {
    kustoName: kustoName
    sku: sku
    tier: tier
    adminGroups: adminGroups
    viewerGroups: viewerGroups
  }
}

// 2. Databases
module serviceLogs 'database.bicep' = {
  name: 'servicelogs'
  params: {
    kustoName: kustoName
    databaseName: db.serviceLogs
    softDeletePeriod: 'P14D'
    hotCachePeriod: 'P2D'
  }
  dependsOn: [cluster]
}

module customerLogs 'database.bicep' = {
  name: 'customerLogs'
  params: {
    kustoName: kustoName
    databaseName: db.customerLogs
    softDeletePeriod: 'P14D'
    hotCachePeriod: 'P2D'
  }
  dependsOn: [serviceLogs]
}

// 3. Create Tables
module serviceLogsTables 'script.bicep' = [
  for tableName in objectKeys(allServiceLogsTablesKQL): {
    name: 'serviceLogsTablesScript-${tableName}'
    params: {
      kustoName: kustoName
      databaseName: db.serviceLogs
      scriptName: tableName
      scriptContent: allServiceLogsTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [serviceLogs]
  }
]

module customerLogsTables 'script.bicep' = [
  for tableName in objectKeys(allCustomerLogsTablesKQL): {
    name: 'customerLogsTablesScript-${tableName}'
    params: {
      kustoName: kustoName
      databaseName: db.customerLogs
      scriptName: tableName
      scriptContent: allCustomerLogsTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [customerLogs]
  }
]

// 4. User-add scripts per database (one script resource per dSTS group)
module databaseUserScripts 'database-users.bicep' = [
  for (database, i) in databases: {
    name: '${database}-databaseUserScripts-${i}'
    params: {
      kustoName: kustoName
      databaseName: database
      dstsGroups: dstsGroups
      continueOnErrors: false
    }
    dependsOn: database == db.customerLogs ? [customerLogs] : [serviceLogs]
  }
]

// 5. Remove the caller principal
// THIS MUST BE THE LAST SCRIPT TO RUN
module removePermission 'script.bicep' = [
  for (database, i) in databases: {
    name: '${database}-removePermission-${i}'
    params: {
      kustoName: kustoName
      databaseName: databases[i]
      scriptName: 'removePermissionScript'
      scriptContent: dummyScript
      principalPermissionsAction: 'RemovePermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [
      databaseUserScripts
      serviceLogsTables
      customerLogsTables
    ]
  }
]

// Outputs mirror original contract
output id string = cluster.outputs.id
