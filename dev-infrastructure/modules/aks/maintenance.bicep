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
          {
            start: '2025-11-16'
            end: '2025-11-22'
          }
          {
            start: '2025-11-24'
            end: '2025-12-03'
          }
          {
            start: '2025-12-22'
            end: '2026-01-13'
          }
          {
            start: '2026-02-16'
            end: '2026-02-20'
          }
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
