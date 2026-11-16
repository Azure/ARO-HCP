@description('Azure Region Location')
param location string = resourceGroup().location

@description('The SKU of the cluster')
param sku string

@description('Tier used')
param tier string = 'Basic'

@description('List of dSTS groups')
param dstsGroups array

@description('Name of the service logs database.')
param serviceLogsDatabase string

@description('Name of the hosted control plane logs database.')
param hostedControlPlaneLogsDatabase string

@description('Name of the consolidated logs database (AllAroHcpLogs). Migration step 1: hosts the rawLogs landing table + non-transactional routing update policies that fan out into the tab_* typed tables, plus the legacy-named compatibility views that union the new tab_* tables with the legacy ServiceLogs/HostedControlPlaneLogs tables.')
param allAroHcpLogsDatabase string

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
  hostedControlPlaneLogs: hostedControlPlaneLogsDatabase
  monitoringEvents: monitoringEventsDatabase
  allAroHcpLogs: allAroHcpLogsDatabase
}

var databases = [db.serviceLogs, db.hostedControlPlaneLogs, db.monitoringEvents, db.allAroHcpLogs]
var hasGrafana = enableGrafanaIntegration && grafanaPrincipalId != '' && grafanaPrincipalId != '__grafanaPrincipalId__'

var dummyScript = '.create-or-alter function with (docstring = \'dummy function to run last and to remove permission\') dummyFunction() {print \'dummy\'}'

var allServiceLogsTablesKQL = {
  backendLogs: loadTextContent('tables/backendLogs.kql')
  kubeApplierLogs: loadTextContent('tables/kubeApplierLogs.kql')
  containerlogs: loadTextContent('tables/containerLogs.kql')
  fleetLogs: loadTextContent('tables/fleetLogs.kql')
  frontendLogs: loadTextContent('tables/frontendLogs.kql')
  clustersServiceLogs: loadTextContent('tables/clustersServiceLogs.kql')
  kubernetesEvents: loadTextContent('tables/kubernetesEvents.kql')
  aksEvents: loadTextContent('tables/aksEvents.kql')
  systemdLogs: loadTextContent('tables/systemdLogs.kql')
  azureVnetLogs: loadTextContent('tables/azureVnetLogs.kql')
  resourceSnapshots: loadTextContent('tables/kubernetesResourceSnapshots.kql')
  cosmosResourceSnapshots: loadTextContent('tables/cosmosResourceSnapshots.kql')
  ciJobOutcomes: loadTextContent('tables/ciJobOutcomes.kql')
  ciDiscoveredJobs: loadTextContent('tables/ciDiscoveredJobs.kql')
  ciProcessedJobs: loadTextContent('tables/ciProcessedJobs.kql')
  ciTestNames: loadTextContent('tables/ciTestNames.kql')
  ciTestResults: loadTextContent('tables/ciTestResults.kql')
  ciFailurePatterns: loadTextContent('tables/ciFailurePatterns.kql')
  maestroMqttConnections: loadTextContent('tables/maestroMqttConnections.kql')
}

var allCustomerLogsTablesKQL = {
  containerlogs: loadTextContent('tables/containerLogs.kql')
  kubernetesEvents: loadTextContent('tables/kubernetesEvents.kql')
}

var allMonitoringEventsTablesKQL = {
  alertEvents: loadTextContent('tables/alertEvents.kql')
}

// AllAroHcpLogs routing foundation (migration step 1): the rawLogs landing table and
// the service-routing helper functions. These MUST be created before the typed tables,
// whose non-transactional update policies reference rawLogs and the routing helpers;
// the routingTables module dependsOn routingFoundation to guarantee that ordering.
var routingFoundationKQL = {
  rawLogs: loadTextContent('tablesv2/routing/00_rawLogs.kql')
  serviceRouting: loadTextContent('tablesv2/routing/01_serviceRouting.kql')
}

// AllAroHcpLogs typed target tables (migration step 1): each file creates a tab_*
// physical table (schema identical to its legacy same-named table), the rawToX()
// transform function, and the non-transactional update policy binding the transform
// to the rawLogs landing table.
var routingTablesKQL = {
  hcpContainerLogs: loadTextContent('tablesv2/routing/hcpContainerLogs.kql')
  frontendLogs: loadTextContent('tablesv2/routing/frontendLogs.kql')
  backendLogs: loadTextContent('tablesv2/routing/backendLogs.kql')
  kubeApplierLogs: loadTextContent('tablesv2/routing/kubeApplierLogs.kql')
  clustersServiceLogs: loadTextContent('tablesv2/routing/clustersServiceLogs.kql')
  fleetLogs: loadTextContent('tablesv2/routing/fleetLogs.kql')
  kubernetesEvents: loadTextContent('tablesv2/routing/kubernetesEvents.kql')
  cosmosResourceSnapshots: loadTextContent('tablesv2/routing/cosmosResourceSnapshots.kql')
  kubernetesResourceSnapshots: loadTextContent('tablesv2/routing/kubernetesResourceSnapshots.kql')
  systemdLogs: loadTextContent('tablesv2/routing/systemdLogs.kql')
  azureVnetLogs: loadTextContent('tablesv2/routing/azureVnetLogs.kql')
  containerLogs: loadTextContent('tablesv2/routing/containerLogs.kql')
}

// AllAroHcpLogs legacy-named compatibility views (migration step 1): each view=true
// function inherits a legacy table name and unions the new tab_* table with the
// legacy ServiceLogs/HostedControlPlaneLogs table, so existing readers keep working
// unchanged and see both new and pre-cutover data. Created after the tab_* tables.
var routingViewsKQL = {
  hcpContainerLogs: loadTextContent('tablesv2/views/hcpContainerLogs.kql')
  frontendLogs: loadTextContent('tablesv2/views/frontendLogs.kql')
  backendLogs: loadTextContent('tablesv2/views/backendLogs.kql')
  kubeApplierLogs: loadTextContent('tablesv2/views/kubeApplierLogs.kql')
  clustersServiceLogs: loadTextContent('tablesv2/views/clustersServiceLogs.kql')
  fleetLogs: loadTextContent('tablesv2/views/fleetLogs.kql')
  kubernetesEvents: loadTextContent('tablesv2/views/kubernetesEvents.kql')
  cosmosResourceSnapshots: loadTextContent('tablesv2/views/cosmosResourceSnapshots.kql')
  kubernetesResourceSnapshots: loadTextContent('tablesv2/views/kubernetesResourceSnapshots.kql')
  systemdLogs: loadTextContent('tablesv2/views/systemdLogs.kql')
  azureVnetLogs: loadTextContent('tablesv2/views/azureVnetLogs.kql')
  containerLogs: loadTextContent('tablesv2/views/containerLogs.kql')
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

module monitoringEvents 'database.bicep' = {
  name: 'monitoringEvents'
  params: {
    location: location
    kustoName: kustoName
    databaseName: db.monitoringEvents
    softDeletePeriod: 'P90D'
    hotCachePeriod: 'P2D'
  }
  dependsOn: [hostedControlPlaneLogs]
}

// Migration step 1: the new consolidated logs database. Default retention matches
// ServiceLogs (P90D); the shorter HCP retention is applied per-table on
// tab_hcpContainerLogs (14d) inside its routing script.
module allAroHcpLogs 'database.bicep' = {
  name: 'allarohcplogs'
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

// 3b. AllAroHcpLogs routing foundation (rawLogs landing table + routing helpers).
// Runs before the typed tables so their update policies can reference rawLogs and
// the routing helper functions.
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
    dependsOn: [allAroHcpLogs]
  }
]

// 3c. AllAroHcpLogs typed target tables (tab_*) + rawToX() transforms + update policies.
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

// 3d. AllAroHcpLogs legacy-named compatibility views (union new tab_* + legacy DBs).
// Runs after the tab_* tables so the view bodies reference existing local tables.
module routingViews 'script.bicep' = [
  for viewName in objectKeys(routingViewsKQL): {
    name: 'routingViewsScript-${viewName}'
    params: {
      kustoName: kustoName
      databaseName: db.allAroHcpLogs
      scriptName: '${viewName}View'
      scriptContent: routingViewsKQL[viewName]
      principalPermissionsAction: 'RetainPermissionOnScriptCompletion'
      continueOnErrors: false
    }
    dependsOn: [routingTables]
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
    dependsOn: database == db.hostedControlPlaneLogs
      ? [hostedControlPlaneLogs]
      : database == db.monitoringEvents
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
// KustoEntityGroups pipeline step (kustoctl) sync entity groups on all
// databases (ServiceLogs, HostedControlPlaneLogs, MonitoringEvents, AllAroHcpLogs)
// after deployment.
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
      monitoringEventsTables
      routingFoundation
      routingTables
      routingViews
      grafanaServiceLogsAccess
    ]
  }
]

// Outputs mirror original contract
output id string = cluster.outputs.id
