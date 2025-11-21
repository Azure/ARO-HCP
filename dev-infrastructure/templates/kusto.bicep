@description('Azure Region Location')
param location string = resourceGroup().location

@description('The SKU of the cluster')
param sku string

@description('Tier used')
param tier string

@description('Toggle if instance should be created/managed')
param manageInstance bool

@description('Environment name')
param environmentName string

@description('Name of the service logs database.')
param serviceLogsDatabase string

@description('Name of the hosted control plane logs database.')
param hostedControlPlaneLogsDatabase string

@description('CSV seperated list of groups to assign admin in the Kusto cluster')
param adminGroups string

@description('CSV seperated list of groups to assign viewer in the Kusto cluster')
param viewerGroups string

@description('Geo short ID of the region')
param geoShortId string

module kusto '../modules/logs/kusto/main.bicep' = if (manageInstance) {
  name: 'kusto-${location}'
  params: {
    dstsGroups: []
    sku: sku
    tier: tier
    serviceLogsDatabase: serviceLogsDatabase
    hostedControlPlaneLogsDatabase: hostedControlPlaneLogsDatabase
    adminGroups: adminGroups
    viewerGroups: viewerGroups
    geoShortId: geoShortId
    environmentName: environmentName
  }
}
