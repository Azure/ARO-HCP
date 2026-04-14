@description('Azure Region Location')
param location string = resourceGroup().location

@description('The SKU of the cluster')
param sku string

@description('Tier used')
param tier string

@description('Toggle if instance should be created/managed')
param manageInstance bool

@description('Name of the service logs database.')
param serviceLogsDatabase string

@description('Name of the hosted control plane logs database.')
param hostedControlPlaneLogsDatabase string

@description('CSV separated list of groups to assign admin in the Kusto cluster')
param adminGroups string

@description('CSV separated list of groups to assign viewer in the Kusto cluster')
param viewerGroups string

@description('CSV separated list of identities (apps/managed identities) to assign viewer in the Kusto cluster')
param viewerIdentities string = ''

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

module kusto '../modules/logs/kusto/main.bicep' = if (manageInstance) {
  name: 'kusto-${location}'
  params: {
    location: location
    kustoName: kustoName
    dstsGroups: []
    sku: sku
    tier: tier
    serviceLogsDatabase: serviceLogsDatabase
    hostedControlPlaneLogsDatabase: hostedControlPlaneLogsDatabase
    adminGroups: adminGroups
    viewerGroups: viewerGroups
    viewerIdentities: viewerIdentities
    autoScaleMin: autoScaleMin
    autoScaleMax: autoScaleMax
    enableAutoScale: enableAutoScale
    crossClusterServiceLogsScript: crossClusterServiceLogsScript
    crossClusterHostedControlPlaneLogsScript: crossClusterHostedControlPlaneLogsScript
    grafanaResourceId: grafanaResourceId
  }
}

output kustoUri string = manageInstance ? kusto.outputs.kustoUri : ''
