@description('Is this alerting for dev environment? Only set in non MSFT environments!')
param devAlerting bool

@description('Comma seperated list of action groups for Sev 1 alerts.')
param sev1ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 2 alerts.')
param sev2ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 3 alerts.')
param sev3ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 4 alerts.')
param sev4ActionGroupIDs string

var sev1ActionGroups = [for eag in split(sev1ActionGroupIDs, ','): {}]
var sev2ActionGroups = [for eag in split(sev2ActionGroupIDs, ','): {}]
var sev3ActionGroups = [for eag in split(sev3ActionGroupIDs, ','): {}]
var sev4ActionGroups = [for eag in split(sev4ActionGroupIDs, ','): {}]

resource serviceLifeCycleEmail 'Microsoft.Insights/actionGroups@2023-01-01' = {
  name: 'emailAroHCPServiceLifeCycleTeam'
  location: 'Global'
  properties: {
    groupShortName: 'emailSLC'
    enabled: true
    emailReceivers: [
      {
        name: 'aro-hcp-service-lifecycle-team'
        emailAddress: 'aro-hcp-service-lifecycle-team@redhat.com'
        useCommonAlertSchema: true
      }
    ]
  }
}

var devSev1ActionGroups = devAlerting ? [serviceLifeCycleEmail.id] : []
var devSev2ActionGroups = devAlerting ? [serviceLifeCycleEmail.id] : []
var devSev3ActionGroups = devAlerting ? [serviceLifeCycleEmail.id] : []
var devSev4ActionGroups = devAlerting ? [serviceLifeCycleEmail.id] : []

output allSev1ActionGroups array = union(sev1ActionGroups, devSev1ActionGroups)
output allSev2ActionGroups array = union(sev2ActionGroups, devSev2ActionGroups)
output allSev3ActionGroups array = union(sev3ActionGroups, devSev3ActionGroups)
output allSev4ActionGroups array = union(sev4ActionGroups, devSev4ActionGroups)
