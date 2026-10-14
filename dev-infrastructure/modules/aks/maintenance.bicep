@description('AKS cluster receiving the shared maintenance policy')
param aksClusterName string

resource aksCluster 'Microsoft.ContainerService/managedClusters@2026-04-02-preview' existing = {
  name: aksClusterName
}

resource maintenanceWindows 'Microsoft.ContainerService/managedClusters/maintenanceConfigurations@2025-08-02-preview' = [
  for maintenanceType in ['default', 'aksManagedAutoUpgradeSchedule', 'aksManagedNodeOSUpgradeSchedule']: {
    parent: aksCluster
    name: maintenanceType
    properties: {
      maintenanceWindow: {
        durationHours: 10
        startTime: '22:00'
        notAllowedDates: [
          // {
          //   start: 'yyyy-mm-dd'
          //   end: 'yyyy-mm-dd'
          // }
        ]
        schedule: {
          weekly: {
            dayOfWeek: 'Saturday'
            intervalWeeks: 1
          }
        }
      }
    }
  }
]
