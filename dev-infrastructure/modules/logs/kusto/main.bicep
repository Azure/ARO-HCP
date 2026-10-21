@description('Azure Region Location')
param location string = resourceGroup().location

@description('The SKU of the cluster')
param sku string

@description('Tier used')
param tier string = 'Basic'

@description('List of dSTS groups')
param dstsGroups array

@description('Name of the service logs database. Retained for the Event-Hub (aksEvents/kubeAudit, maestroMqttConnections) and CI (ci*) tables only; all forwarder logs now route to AllAroHcpLogs.')
param serviceLogsDatabase string

@description('Name of the monitoring events database.')
param monitoringEventsDatabase string

@description('Name of the single all-logs database (AllAroHcpLogs); routing/transform is done by ADX update policies. See docs/logging.md.')
param allAroHcpLogsDatabase string

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

@description('Whether to grant the global Grafana identity Viewer access to ServiceLogs')
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
  serviceLogs: serviceLogsDatabase
  monitoringEvents: monitoringEventsDatabase
  allAroHcpLogs: allAroHcpLogsDatabase
}

var databases = [db.serviceLogs, db.monitoringEvents, db.allAroHcpLogs]
var hasGrafana = enableGrafanaIntegration && grafanaPrincipalId != '' && grafanaPrincipalId != '__grafanaPrincipalId__'

var dummyScript = '.create-or-alter function with (docstring = \'dummy function to run last and to remove permission\') dummyFunction() {print \'dummy\'}'

// ServiceLogs now hosts ONLY the out-of-scope Event-Hub (aksEvents/kubeAudit,
// maestroMqttConnections) and CI (ci*) tables. All forwarder-fed log tables that
// used to live here (frontend/backend/clusters-service/fleet/kube-applier/
// container/kubernetesEvents/systemd/azureVnet/*ResourceSnapshots) moved to the
// AllAroHcpLogs database and are no longer created here. The pre-existing live
// tables are left untouched (incremental deployment does not drop them); physical
// decommission is a separate Phase 4 cleanup (see docs/logging.md).
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

// AllAroHcpLogs database (see docs/logging.md). The foundation scripts (raw
// landing table + routing helper functions) MUST be created before the target
// tables, whose transactional update policies reference rawLogs and the routing
// helpers; the table scripts dependsOn the foundation scripts to guarantee this.
var allAroHcpLogsFoundationKQL = {
  rawLogs: loadTextContent('tables/allarohcplogs/00_rawLogs.kql')
  serviceRouting: loadTextContent('tables/allarohcplogs/01_serviceRouting.kql')
}

var allAroHcpLogsTablesKQL = {
  hcpContainerLogs: loadTextContent('tables/allarohcplogs/hcpContainerLogs.kql')
  frontendLogs: loadTextContent('tables/allarohcplogs/frontendLogs.kql')
  backendLogs: loadTextContent('tables/allarohcplogs/backendLogs.kql')
  kubeApplierLogs: loadTextContent('tables/allarohcplogs/kubeApplierLogs.kql')
  clustersServiceLogs: loadTextContent('tables/allarohcplogs/clustersServiceLogs.kql')
  fleetLogs: loadTextContent('tables/allarohcplogs/fleetLogs.kql')
  kubernetesEvents: loadTextContent('tables/allarohcplogs/kubernetesEvents.kql')
  cosmosResourceSnapshots: loadTextContent('tables/allarohcplogs/cosmosResourceSnapshots.kql')
  kubernetesResourceSnapshots: loadTextContent('tables/allarohcplogs/kubernetesResourceSnapshots.kql')
  systemdLogs: loadTextContent('tables/allarohcplogs/systemdLogs.kql')
  azureVnetLogs: loadTextContent('tables/allarohcplogs/azureVnetLogs.kql')
  containerLogs: loadTextContent('tables/allarohcplogs/containerLogs.kql')
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
    databaseName: db.serviceLogs
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

// AllAroHcpLogs: DB default 90d soft delete / 2d hot cache (R1). HCP logs get a
// shorter 14d retention via the table-level policy on hcpContainerLogs (R5).
module allAroHcpLogs 'database.bicep' = {
  name: 'allAroHcpLogs'
  params: {
    location: location
    kustoName: kustoName
    databaseName: db.allAroHcpLogs
    softDeletePeriod: 'P90D'
    hotCachePeriod: 'P2D'
  }
  dependsOn: [monitoringEvents]
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

// 3b. AllAroHcpLogs: foundation scripts (raw landing table + routing helpers)
// run first so the target-table update policies can reference rawLogs and the
// routing helper functions.
module allAroHcpLogsFoundation 'script.bicep' = [
  for scriptName in objectKeys(allAroHcpLogsFoundationKQL): {
    name: 'allAroHcpLogsFoundation-${scriptName}'
    params: {
      kustoName: kustoName
      databaseName: db.allAroHcpLogs
      scriptName: scriptName
      scriptContent: allAroHcpLogsFoundationKQL[scriptName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [allAroHcpLogs]
  }
]

module allAroHcpLogsTables 'script.bicep' = [
  for tableName in objectKeys(allAroHcpLogsTablesKQL): {
    name: 'allAroHcpLogsTablesScript-${tableName}'
    params: {
      kustoName: kustoName
      databaseName: db.allAroHcpLogs
      scriptName: tableName
      scriptContent: allAroHcpLogsTablesKQL[tableName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [allAroHcpLogsFoundation]
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
    dependsOn: database == db.monitoringEvents
      ? [monitoringEvents]
      : database == db.allAroHcpLogs ? [allAroHcpLogs] : [serviceLogs]
  }
]

// 5. Grafana ServiceLogs access
module grafanaServiceLogsAccess 'grant-access.bicep' = if (hasGrafana) {
  name: 'grafana-serviceLogs-viewer'
  params: {
    kustoName: kustoName
    databaseName: db.serviceLogs
    readAccessPrincipalIds: [grafanaPrincipalId]
  }
  dependsOn: [serviceLogsTables]
}

// 5b. Grafana AllAroHcpLogs access (new single database).
module grafanaAllAroHcpLogsAccess 'grant-access.bicep' = if (hasGrafana) {
  name: 'grafana-allAroHcpLogs-viewer'
  params: {
    kustoName: kustoName
    databaseName: db.allAroHcpLogs
    readAccessPrincipalIds: [grafanaPrincipalId]
  }
  dependsOn: [allAroHcpLogsTables]
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
// KustoEntityGroups pipeline step (kustoctl) sync entity groups on all three
// databases (ServiceLogs, MonitoringEvents and AllAroHcpLogs) after deployment.
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
      allAroHcpLogsFoundation
      allAroHcpLogsTables
      grafanaServiceLogsAccess
      grafanaAllAroHcpLogsAccess
    ]
  }
]

// Outputs mirror original contract
output id string = cluster.outputs.id
