@description('Workspace region, independent of the resource group and consuming clusters')
param location string

param servicesNamePrefix string

@minValue(0)
@maxValue(800)
param servicesCount int

param hcpsNamePrefix string

@minValue(0)
@maxValue(800)
param hcpsCount int

module services '../modules/metrics/monitor.bicep' = [
  for i in range(0, servicesCount): {
    name: '${servicesNamePrefix}-${i}'
    params: {
      monitorName: '${servicesNamePrefix}-${i}'
      location: location
      purpose: 'services'
      grafanaResourceId: ''
    }
  }
]

module hcps '../modules/metrics/monitor.bicep' = [
  for i in range(0, hcpsCount): {
    name: '${hcpsNamePrefix}-${i}'
    params: {
      monitorName: '${hcpsNamePrefix}-${i}'
      location: location
      purpose: 'hcps'
      grafanaResourceId: ''
    }
  }
]
