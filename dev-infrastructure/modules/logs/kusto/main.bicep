import * as res from '../../resource.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

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

@description('CSV separated list of groups to assign admin in the Kusto cluster')
param adminGroups string

@description('CSV separated list of groups to assign viewer in the Kusto cluster')
param viewerGroups string

@description('Name of the Kusto cluster to create')
param kustoName string

@description('Minimum number of nodes for autoscale')
param autoScaleMin int

@description('Maximum number of nodes for autoscale')
param autoScaleMax int

@description('Toggle if autoscale should be enabled')
param enableAutoScale bool

@description('Optional cross-cluster ServiceLogs Kusto script content.')
@secure()
param crossClusterServiceLogsScript string = ''

@description('Optional cross-cluster HostedControlPlaneLogs Kusto script content.')
@secure()
param crossClusterHostedControlPlaneLogsScript string = ''

@description('Optional: Grafana resource ID for database-level Viewer access')
param grafanaResourceId string = ''

var db = {
  serviceLogs: serviceLogsDatabase
  hostedControlPlaneLogs: hostedControlPlaneLogsDatabase
}

var databases = [db.serviceLogs, db.hostedControlPlaneLogs]
var hasGrafana = grafanaResourceId != ''
var grafanaRef = hasGrafana ? res.grafanaRefFromId(grafanaResourceId) : null

var dummyScript = '.create-or-alter function with (docstring = \'dummy function to run last and to remove permission\') dummyFunction() {print \'dummy\'}'

resource grafana 'Microsoft.Dashboard/grafana@2024-10-01' existing = if (hasGrafana) {
  name: grafanaRef!.name
  scope: resourceGroup(grafanaRef!.resourceGroup.subscriptionId, grafanaRef!.resourceGroup.name)
}

var allServiceLogsTablesKQL = {
  backendLogs: loadTextContent('tables/backendLogs.kql')
  containerlogs: loadTextContent('tables/containerLogs.kql')
  frontendLogs: loadTextContent('tables/frontendLogs.kql')
  clustersServiceLogs: loadTextContent('tables/clustersServiceLogs.kql')
  kubernetesEvents: loadTextContent('tables/kubernetesEvents.kql')
  aksEvents: loadTextContent('tables/aksEvents.kql')
  systemdLogs: loadTextContent('tables/systemdLogs.kql')
}

var allCustomerLogsTablesKQL = {
  containerlogs: loadTextContent('tables/containerLogs.kql')
  kubernetesEvents: loadTextContent('tables/kubernetesEvents.kql')
}

var deployCrossClusterServiceLogsScript = !empty(crossClusterServiceLogsScript)
var deployCrossClusterHostedControlPlaneLogsScript = !empty(crossClusterHostedControlPlaneLogsScript)

// 1. Cluster
module cluster 'cluster.bicep' = {
  name: 'cluster'
  params: {
    location: location
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
    location: location
    kustoName: kustoName
    databaseName: db.serviceLogs
    softDeletePeriod: 'P90D'
    hotCachePeriod: 'P2D'
  }
  dependsOn: [cluster]
}

module hostedControlPlaneLogs 'database.bicep' = {
  name: 'hostedControlPlaneLogs'
  params: {
    location: location
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

// 5. Cross-cluster query scripts (executed when their script content is provided)
module crossClusterServiceLogsQueryScript 'script.bicep' = if (deployCrossClusterServiceLogsScript) {
  name: 'crossClusterServiceLogsScript'
  params: {
    kustoName: kustoName
    databaseName: db.serviceLogs
    scriptName: 'crossClusterQueries'
    scriptContent: crossClusterServiceLogsScript
    principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
    continueOnErrors: false
  }
  dependsOn: [
    databaseUserScripts
    serviceLogsTables
    hostedControlPlaneLogsTables
  ]
}

module crossClusterHostedControlPlaneLogsQueryScript 'script.bicep' = if (deployCrossClusterHostedControlPlaneLogsScript) {
  name: 'crossClusterHostedControlPlaneLogsScript'
  params: {
    kustoName: kustoName
    databaseName: db.hostedControlPlaneLogs
    scriptName: 'crossClusterQueries'
    scriptContent: crossClusterHostedControlPlaneLogsScript
    principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
    continueOnErrors: false
  }
  dependsOn: [
    databaseUserScripts
    serviceLogsTables
    hostedControlPlaneLogsTables
  ]
}

// 6. Grafana service logs access
module grafanaServiceLogsAccess 'grant-access.bicep' = if (hasGrafana) {
  name: 'grafana-serviceLogs-viewer'
  params: {
    kustoName: kustoName
    databaseName: db.serviceLogs
    readAccessPrincipalIds: [grafana!.identity.principalId]
  }
  dependsOn: [serviceLogsTables]
}

// 7. Remove the caller principal
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
      crossClusterServiceLogsQueryScript
      crossClusterHostedControlPlaneLogsQueryScript
      grafanaServiceLogsAccess
    ]
  }
]

// Outputs mirror original contract
output id string = cluster.outputs.id
output kustoUri string = cluster.outputs.uri
