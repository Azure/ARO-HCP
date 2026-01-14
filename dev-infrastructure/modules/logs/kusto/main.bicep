@description('The SKU of the cluster')
param sku string = 'Standard_D12_v2'

@description('Tier used')
param tier string = 'Basic'

@description('List of dSTS groups')
param dstsGroups array

@description('Name of the service logs database.')
param serviceLogsDatabase string

@description('Name of the hosted control plane logs database.')
param hostedControlPlaneLogsDatabase string

@description('CSV seperated list of groups to assign admin in the Kusto cluster')
param adminGroups string

@description('CSV seperated list of groups to assign viewer in the Kusto cluster')
param viewerGroups string

@description('Name of the Kusto cluster to create')
param kustoName string

@description('Minimum number of nodes for autoscale')
param autoScaleMin int

@description('Maximum number of nodes for autoscale')
param autoScaleMax int

@description('Toggle if autoscale should be enabled')
param enableAutoScale bool

var db = {
  serviceLogs: serviceLogsDatabase
  hostedControlPlaneLogs: hostedControlPlaneLogsDatabase
}

var databases = [db.serviceLogs, db.hostedControlPlaneLogs]

var dummyScript = '.create-or-alter function with (docstring = \'dummy function to run last and to remove permission\') dummyFunction() {print \'dummy\'}'

var allServiceLogsTablesKQL = {
  backendLogs: loadTextContent('tables/backendLogs.kql')
  containerlogs: loadTextContent('tables/containerLogs.kql')
  frontendLogs: loadTextContent('tables/frontendLogs.kql')
  clustersServiceLogs: loadTextContent('tables/clustersServiceLogs.kql')
  kubernetesEvents: loadTextContent('tables/kubernetesEvents.kql')
}

var allCustomerLogsTablesKQL = {
  containerlogs: loadTextContent('tables/containerLogs.kql')
  kubernetesEvents: loadTextContent('tables/kubernetesEvents.kql')
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
    autoScaleMin: autoScaleMin
    autoScaleMax: autoScaleMax
    enableAutoScale: enableAutoScale
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

module hostedControlPlaneLogs 'database.bicep' = {
  name: 'hostedControlPlaneLogs'
  params: {
    kustoName: kustoName
    databaseName: db.hostedControlPlaneLogs
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

module hostedControlPlaneLogsTables 'script.bicep' = [
  for tableName in objectKeys(allCustomerLogsTablesKQL): {
    name: 'customerLogsTablesScript-${tableName}'
    params: {
      kustoName: kustoName
      databaseName: db.hostedControlPlaneLogs
      scriptName: tableName
      scriptContent: allCustomerLogsTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [hostedControlPlaneLogs]
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
    dependsOn: database == db.hostedControlPlaneLogs ? [hostedControlPlaneLogs] : [serviceLogs]
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
      hostedControlPlaneLogsTables
    ]
  }
]

// Outputs mirror original contract
output id string = cluster.outputs.id
