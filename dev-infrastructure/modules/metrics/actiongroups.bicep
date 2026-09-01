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

@description('Name of the ICM Action Group')
param icmActionGroupNameRP string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameRP string

@description('ICM routing ID')
param icmRoutingIdRP string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledRP string

@description('Name of the ICM Action Group')
param icmActionGroupNameMSFT string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameMSFT string

@description('ICM routing ID')
param icmRoutingIdMSFT string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledMSFT string

@description('Name of the ICM Action Group')
param icmActionGroupNameDEV string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortNameDEV string

@description('ICM routing ID')
param icmRoutingIdDEV string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledDEV string

@description('Indicates if alerting should be enabled for this region. When true, action groups will be enabled.')
param alertingEnabled bool

resource icmsre 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'icm-action-group-sre'
  location: 'global'
  properties: {
    enabled: alertingEnabled
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
    enabled: alertingEnabled
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

resource icmmsft 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'icm-action-group-msft'
  location: 'global'
  properties: {
    enabled: alertingEnabled
    groupShortName: icmActionGroupShortNameMSFT
    incidentReceivers: [
      {
        name: icmActionGroupNameMSFT
        incidentManagementService: 'Icm'
        connection: {
          name: icmConnectionName
          id: icmConnectionId
        }
        mappings: {
          'Icm.occurringlocation.environment': icmEnvironment
          'Icm.routingid': icmRoutingIdMSFT
          'Icm.automitigationenabled': icmAutomitigationEnabledMSFT
        }
      }
    ]
  }
}

resource icmrp 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'icm-action-group-rp'
  location: 'global'
  properties: {
    enabled: alertingEnabled
    groupShortName: icmActionGroupShortNameRP
    incidentReceivers: [
      {
        name: icmActionGroupNameRP
        incidentManagementService: 'Icm'
        connection: {
          name: icmConnectionName
          id: icmConnectionId
        }
        mappings: {
          'Icm.occurringlocation.environment': icmEnvironment
          'Icm.routingid': icmRoutingIdRP
          'Icm.automitigationenabled': icmAutomitigationEnabledRP
        }
      }
    ]
  }
}

resource icmdev 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'icm-action-group-dev'
  location: 'global'
  properties: {
    enabled: alertingEnabled
    groupShortName: icmActionGroupShortNameDEV
    incidentReceivers: [
      {
        name: icmActionGroupNameDEV
        incidentManagementService: 'Icm'
        connection: {
          name: icmConnectionName
          id: icmConnectionId
        }
        mappings: {
          'Icm.occurringlocation.environment': icmEnvironment
          'Icm.routingid': icmRoutingIdDEV
          'Icm.automitigationenabled': icmAutomitigationEnabledDEV
        }
      }
    ]
  }
}

output actionGroupsSRE string = icmsre.id
output actionGroupsSL string = icmsl.id
output actionGroupsRP string = icmrp.id
output actionGroupsMSFT string = icmmsft.id
output actionGroupsDEV string = icmdev.id
