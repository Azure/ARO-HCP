@description('Comma seperated list of email notifications. Only set in non MSFT environments!')
param devAlertingEmails string

@description('Comma seperated list of action groups for Sev 1 alerts.')
param sev1ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 2 alerts.')
param sev2ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 3 alerts.')
param sev3ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 4 alerts.')
param sev4ActionGroupIDs string

var sev1ActionGroups = [for eag in split(sev1ActionGroupIDs, ','): eag]
var sev2ActionGroups = [for eag in split(sev2ActionGroupIDs, ','): eag]
var sev3ActionGroups = [for eag in split(sev3ActionGroupIDs, ','): eag]
var sev4ActionGroups = [for eag in split(sev4ActionGroupIDs, ','): eag]

var emailAdresses = [for e in split(devAlertingEmails, ','): e]
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

var actionGroupsCreated = [for (j, index) in emailAdresses: emailActions[index].id]

output allSev1ActionGroups array = union(sev1ActionGroups, actionGroupsCreated)
output allSev2ActionGroups array = union(sev2ActionGroups, actionGroupsCreated)
output allSev3ActionGroups array = union(sev3ActionGroups, actionGroupsCreated)
output allSev4ActionGroups array = union(sev4ActionGroups, actionGroupsCreated)
