@description('Azure Region Location')
param location string = resourceGroup().location

@description('The SKU of the cluster')
param sku string

@description('Tier used')
param tier string = 'Basic'

@description('List of dSTS groups')
param dstsGroups array

@description('Name of the single consolidated logs database (AllAroHcpLogs). Hosts the Event-Hub (aksEvents/kubeAudit, maestroMqttConnections) and CI (ci*) tables plus the forwarder routing table set (rawLogs landing table + routing helpers + typed target tables).')
param allAroHcpLogsDatabase string = ''

@description('DEPRECATED, for backward compatibility only. Alias of allAroHcpLogsDatabase, used only as a fallback when allAroHcpLogsDatabase is empty.')
param serviceLogsDatabase string = ''

@description('Name of the monitoring events database.')
param monitoringEventsDatabase string

@description('CSV separated list of groups to assign admin in the Kusto cluster')
param adminGroups string

@description('CSV separated list of groups to assign viewer in the Kusto cluster')
param viewerGroups string

@description('CSV separated list of identities (apps/managed identities) to assign viewer in the Kusto cluster')
param viewerIdentities string = ''

@description('Name of the global rollout MSI granted AllDatabasesAdmin so the KustoEntityGroups pipeline step (kustoctl) can sync entity groups. Empty disables the grant.')
param globalMSIName string = ''

@description('Resource group of the global rollout MSI (same subscription as this deployment). Required when globalMSIName is set.')
param globalMSIResourceGroup string = ''

@description('ARO-HCP environment (int, stg, prod) tagged on the Kusto cluster so kustoctl can scope entity-group discovery per environment.')
param environment string = ''

@description('Name of the Kusto cluster to create')
param kustoName string

@description('ARO-HCP geography short ID used for global resource discovery')
param geoShortId string

@description('Whether to grant the global Grafana identity Viewer access to AllAroHcpLogs')
param enableGrafanaIntegration bool = false

@description('Global Azure Managed Grafana principal ID')
param grafanaPrincipalId string = ''

@description('Minimum number of nodes for autoscale')
param autoScaleMin int

@description('Maximum number of nodes for autoscale')
param autoScaleMax int

@description('Toggle if autoscale should be enabled')
param enableAutoScale bool

var db = {
  allAroHcpLogs: !empty(allAroHcpLogsDatabase) ? allAroHcpLogsDatabase : serviceLogsDatabase
  monitoringEvents: monitoringEventsDatabase
}

var databases = [db.allAroHcpLogs, db.monitoringEvents]
var hasGrafana = enableGrafanaIntegration && grafanaPrincipalId != '' && grafanaPrincipalId != '__grafanaPrincipalId__'

var dummyScript = '.create-or-alter function with (docstring = \'dummy function to run last and to remove permission\') dummyFunction() {print \'dummy\'}'

// AllAroHcpLogs is the single logs database. It hosts both the Event-Hub
// (aksEvents/kubeAudit, maestroMqttConnections) and CI (ci*) tables below and the
// forwarder routing table set (rawLogs landing table + routing helpers + typed
// target tables) created further down. All forwarder logs land in AllAroHcpLogs via
// the rawLogs table and are fanned out to the typed tables by ADX update policies;
// there is no separate AllAroHcpLogs database (see docs/logging.md).
var allServiceLogsTablesKQL = {
  aksEvents: loadTextContent('tables/aksEvents.kql')
  ciJobOutcomes: loadTextContent('tables/ciJobOutcomes.kql')
  ciDiscoveredJobs: loadTextContent('tables/ciDiscoveredJobs.kql')
  ciProcessedJobs: loadTextContent('tables/ciProcessedJobs.kql')
  ciTestNames: loadTextContent('tables/ciTestNames.kql')
  ciTestResults: loadTextContent('tables/ciTestResults.kql')
  maestroMqttConnections: loadTextContent('tables/maestroMqttConnections.kql')
}

var allMonitoringEventsTablesKQL = {
  alertEvents: loadTextContent('tables/alertEvents.kql')
}

// Forwarder routing tables, deployed into the AllAroHcpLogs database (see
// docs/logging.md). The foundation scripts (raw landing table + routing helper
// functions) MUST be created before the target tables, whose (non-transactional)
// update policies reference rawLogs and the routing helpers; the table scripts dependsOn
// the foundation scripts to guarantee this.
var routingFoundationKQL = {
  rawLogs: loadTextContent('tables/routing/00_rawLogs.kql')
  serviceRouting: loadTextContent('tables/routing/01_serviceRouting.kql')
}

var routingTablesKQL = {
  hcpContainerLogs: loadTextContent('tables/routing/hcpContainerLogs.kql')
  frontendLogs: loadTextContent('tables/routing/frontendLogs.kql')
  backendLogs: loadTextContent('tables/routing/backendLogs.kql')
  kubeApplierLogs: loadTextContent('tables/routing/kubeApplierLogs.kql')
  clustersServiceLogs: loadTextContent('tables/routing/clustersServiceLogs.kql')
  fleetLogs: loadTextContent('tables/routing/fleetLogs.kql')
  kubernetesEvents: loadTextContent('tables/routing/kubernetesEvents.kql')
  cosmosResourceSnapshots: loadTextContent('tables/routing/cosmosResourceSnapshots.kql')
  kubernetesResourceSnapshots: loadTextContent('tables/routing/kubernetesResourceSnapshots.kql')
  systemdLogs: loadTextContent('tables/routing/systemdLogs.kql')
  azureVnetLogs: loadTextContent('tables/routing/azureVnetLogs.kql')
  containerLogs: loadTextContent('tables/routing/containerLogs.kql')
}

// 1. Cluster
module cluster 'cluster.bicep' = {
  name: 'cluster'
  params: {
    location: location
    kustoName: kustoName
    geoShortId: geoShortId
    sku: sku
    tier: tier
    adminGroups: adminGroups
    viewerGroups: viewerGroups
    viewerIdentities: viewerIdentities
    globalMSIName: globalMSIName
    globalMSIResourceGroup: globalMSIResourceGroup
    environment: environment
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
    databaseName: db.allAroHcpLogs
    softDeletePeriod: 'P90D'
    hotCachePeriod: 'P2D'
  }
  dependsOn: [cluster]
}

module monitoringEvents 'database.bicep' = {
  name: 'monitoringEvents'
  params: {
    location: location
    kustoName: kustoName
    databaseName: db.monitoringEvents
    softDeletePeriod: 'P90D'
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
      databaseName: db.allAroHcpLogs
      scriptName: tableName
      scriptContent: allServiceLogsTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [serviceLogs]
  }
]

module monitoringEventsTables 'script.bicep' = [
  for tableName in objectKeys(allMonitoringEventsTablesKQL): {
    name: 'monitoringEventsTablesScript-${tableName}'
    params: {
      kustoName: kustoName
      databaseName: db.monitoringEvents
      scriptName: tableName
      scriptContent: allMonitoringEventsTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [monitoringEvents]
  }
]

// 3b. Routing foundation scripts (raw landing table + routing helpers) run first
// so the target-table update policies can reference rawLogs and the routing helper
// functions. Deployed into the AllAroHcpLogs database.
module routingFoundation 'script.bicep' = [
  for scriptName in objectKeys(routingFoundationKQL): {
    name: 'routingFoundation-${scriptName}'
    params: {
      kustoName: kustoName
      databaseName: db.allAroHcpLogs
      scriptName: scriptName
      scriptContent: routingFoundationKQL[scriptName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [serviceLogs]
  }
]

module routingTables 'script.bicep' = [
  for tableName in objectKeys(routingTablesKQL): {
    name: 'routingTablesScript-${tableName}'
    params: {
      kustoName: kustoName
      databaseName: db.allAroHcpLogs
      scriptName: tableName
      scriptContent: routingTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [routingFoundation]
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
    dependsOn: database == db.monitoringEvents ? [monitoringEvents] : [serviceLogs]
  }
]

// 5. Grafana ServiceLogs access (AllAroHcpLogs is the single logs database).
module grafanaServiceLogsAccess 'grant-access.bicep' = if (hasGrafana) {
  name: 'grafana-serviceLogs-viewer'
  params: {
    kustoName: kustoName
    databaseName: db.allAroHcpLogs
    readAccessPrincipalIds: [grafanaPrincipalId]
  }
  dependsOn: [serviceLogsTables, routingTables]
}

// 6. Remove the caller principal
// THIS MUST BE THE LAST SCRIPT TO RUN
// The table scripts above use RetainPermissionOnScriptCompletion, so the
// principal that runs them keeps database Admin on each logs database after
// they finish. This dummy script runs LAST with RemovePermissionOnScriptCompletion
// to strip that retained, per-database Admin so no standing admin accumulates on
// the Kusto databases.
// The strip is database-scoped and only affects that deployment principal; it
// does not touch the cluster-scoped AllDatabasesAdmin principalAssignment that
// cluster.bicep grants the global rollout MSI, which is what lets the
// KustoEntityGroups pipeline step (kustoctl) sync entity groups on both
// databases (AllAroHcpLogs and MonitoringEvents) after deployment.
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
      monitoringEventsTables
      routingFoundation
      routingTables
      grafanaServiceLogsAccess
    ]
  }
]

// Outputs mirror original contract
output id string = cluster.outputs.id
