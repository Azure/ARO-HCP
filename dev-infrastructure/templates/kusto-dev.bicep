@description('Azure Region Location')
param location string = resourceGroup().location

param clusterName string

param svcLogsManagedIdentity string

module devkusto '../modules/logs/kusto.bicep' = {
  name: 'kusto-${clusterName}'
  params: {
    clusterName: clusterName
     capacity: 1
     location: location
     svcLogsManagedIdentity: svcLogsManagedIdentity
  }

}
