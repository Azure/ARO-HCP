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

module kusto '../modules/logs/kusto/main.bicep' = if (manageInstance) {
  name: 'kusto-${location}'
  params: {
    kustoName: kustoName
    dstsGroups: []
    sku: sku
    tier: tier
    serviceLogsDatabase: serviceLogsDatabase
    hostedControlPlaneLogsDatabase: hostedControlPlaneLogsDatabase
    adminGroups: adminGroups
    viewerGroups: viewerGroups
    autoScaleMin: autoScaleMin
    autoScaleMax: autoScaleMax
    enableAutoScale: enableAutoScale
  }
}
