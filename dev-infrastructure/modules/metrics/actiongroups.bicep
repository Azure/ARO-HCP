import { csvToArray } from '../common.bicep'

@description('Comma seperated list of email notifications. Only set in non MSFT environments!')
param devAlertingEmails string

@description('The ICM environment')
param icmEnvironment string

@description('Name of the ICM Action Group')
param icmActionGroupName string

@description('Name of the ICM Action Group')
@maxLength(8)
param icmActionGroupShortName string

@description('ICM routing ID')
param icmRoutingId string

@description('ICM connection Name')
param icmConnectionName string

@description('ICM connection id')
param icmConnectionId string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabled string

resource icm 'Microsoft.Insights/actionGroups@2024-10-01-preview' = if (icmActionGroupName != '') {
  name: 'icm-action-group'
  location: 'global'
  properties: {
    enabled: true
    groupShortName: icmActionGroupShortName
    incidentReceivers: [
      {
        name: icmActionGroupName
        incidentManagementService: 'Icm'
        connection: {
          name: icmConnectionName
          id: icmConnectionId
        }
        mappings: {
          'Icm.occurringlocation.environment': icmEnvironment
          'Icm.routingid': icmRoutingId
          'Icm.automitigationenabled': icmAutomitigationEnabled
        }
      }
    ]
  }
}

var emailAdresses = csvToArray(devAlertingEmails)
resource emailActions 'Microsoft.Insights/actionGroups@2023-01-01' = [
  for email in emailAdresses: {
    name: email
    location: 'Global'
    properties: {
      groupShortName: substring(uniqueString(email), 0, 8)
      enabled: true
      emailReceivers: [
        {
          name: split(email, '@')[0]
          emailAddress: email
          useCommonAlertSchema: true
        }
      ]
    }
  }
]

output actionGroups array = [for (j, index) in emailAdresses: emailActions[index].id]
