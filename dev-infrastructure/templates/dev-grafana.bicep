import { getLocationAvailabilityZonesCSV, determineZoneRedundancy, csvToArray } from '../modules/common.bicep'

@description('Azure Global Location')
param location string

@description('The global msi name')
param globalMSIName string

@description('Global Grafana instance name')
param grafanaName string

@description('The Grafana major version')
param grafanaMajorVersion string

@description('List of grafana role assignments as a space-separated list of items in the format of "principalId/principalType/role"')
param grafanaRoles string

@description('The zone redundant mode of Grafana')
param grafanaZoneRedundantMode string

@description('Cross-tenant security group for Grafana access (format: GroupObjectId;TenantId)')
param crossTenantSecurityGroup string

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)


//
//  G L O B A L   M S I
//

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

//
//   G R A F A N A
//

module grafana '../modules/grafana/instance.bicep' = {
  name: 'grafana'
  params: {
    location: location
    grafanaName: grafanaName
    grafanaMajorVersion: grafanaMajorVersion
    grafanaManagerPrincipalId: globalMSI.properties.principalId
    grafanaRoles: grafanaRoles
    zoneRedundancy: determineZoneRedundancy(locationAvailabilityZoneList, grafanaZoneRedundantMode)
    azureMonitorWorkspaceIds: []
    crossTenantSecurityGroup: crossTenantSecurityGroup
  }
}
