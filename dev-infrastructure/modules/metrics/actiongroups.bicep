@description('ICM connection Name')
param icmConnectionName string

@description('ICM connection id')
param icmConnectionId string

@description('The ICM environment')
param icmEnvironment string

@description('Name of the ICM Action Group')
param icmActionGroupNameSRE string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameSRE string

@description('ICM routing ID')
param icmRoutingIdSRE string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSRE string

@description('Name of the ICM Action Group')
param icmActionGroupNameSL string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameSL string

@description('ICM routing ID')
param icmRoutingIdSL string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSL string

resource icmsre 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'icm-action-group-sre'
  location: 'global'
  properties: {
    enabled: true
    groupShortName: icmActionGroupShortNameSRE
    incidentReceivers: [
      {
        name: icmActionGroupNameSRE
        incidentManagementService: 'Icm'
        connection: {
          name: icmConnectionName
          id: icmConnectionId
        }
        mappings: {
          'Icm.occurringlocation.environment': icmEnvironment
          'Icm.routingid': icmRoutingIdSRE
          'Icm.automitigationenabled': icmAutomitigationEnabledSRE
        }
      }
    ]
  }
}

resource icmsl 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'icm-action-group-sl'
  location: 'global'
  properties: {
    enabled: true
    groupShortName: icmActionGroupShortNameSL
    incidentReceivers: [
      {
        name: icmActionGroupNameSL
        incidentManagementService: 'Icm'
        connection: {
          name: icmConnectionName
          id: icmConnectionId
        }
        mappings: {
          'Icm.occurringlocation.environment': icmEnvironment
          'Icm.routingid': icmRoutingIdSL
          'Icm.automitigationenabled': icmAutomitigationEnabledSL
        }
      }
    ]
  }
}

output actionGroupsSRE string = icmsre.id
output actionGroupsSL string = icmsl.id
