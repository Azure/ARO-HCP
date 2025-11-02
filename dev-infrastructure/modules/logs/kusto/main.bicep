// Orchestrator for Kusto deployment: cluster, databases, and scripts.
// This file replaces the original monolithic kusto.bicep. The legacy kusto.bicep
// now acts as a thin wrapper to maintain backward compatibility with existing pipelines.

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

@description('The principal ID of the Geneva Viewer.')
param genevaViewerPrincipalId string

@description('The tenant ID of the Geneva Viewer.')
param genevaViewerTenantId string

@description('The principal ID of the IcM Viewer.')
param icmViewerPrincipalId string

@description('The tenant ID of the IcM Viewer.')
param icmViewerTenantId string

@description('List of dSTS groups')
param dstsGroups array

@description('The cloud name.')
param cloud string

@description('The environment name. Note that stg is shared with prod.')
param environment string

var db = {
  serviceLogs: 'ServiceLogs'
  customerLogs: 'CustomerLogs'
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
    clusterPrincipals: clusterPrincipals
    dataConnectionName: dataConnectionName
    genevaEnvironment: genevaEnvironment
    rpAccount: rpAccount
    clusterAccount: clusterAccount
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
    genevaViewerPrincipalId: genevaViewerPrincipalId
    genevaViewerTenantId: genevaViewerTenantId
    icmViewerPrincipalId: icmViewerPrincipalId
    icmViewerTenantId: icmViewerTenantId
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
    genevaViewerPrincipalId: genevaViewerPrincipalId
    genevaViewerTenantId: genevaViewerTenantId
    icmViewerPrincipalId: icmViewerPrincipalId
    icmViewerTenantId: icmViewerTenantId
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
    dependsOn: database == db.cluster
      ? [AROClusterLogsModule]
      : database == db.rp
          ? [ARORPLogsModule]
          : database == db.hcpCustomer ? [HCPCustomerLogsModule] : [HCPServiceLogsModule]
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
      databaseUserScripts[i]
      clusterLogsScript
      rpLogsScript
    ]
  }
]

// 5. Insights alerting (action group + metric alert)
module kustoInsights 'insights.bicep' = {
  name: 'kustoInsights'
  params: {
    // Pass the cluster resource ID (module output) to the insights template which uses it as scope
    kustoName: cluster.outputs.id
  }
}

// Outputs mirror original contract
output id string = cluster.outputs.id
output uri string = cluster.outputs.uri
output principalId string = cluster.outputs.principalId
