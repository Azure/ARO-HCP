@description('Name of the Kusto (ADX) cluster hosting the database.')
param kustoName string

@description('Name of the target Kusto database where users will be added.')
param databaseName string

@description('Array of dSTS group objects containing at least name and description properties.')
param dstsGroups array

@description('Whether each individual script should continue on errors.')
param continueOnErrors bool = false

// Emit one script resource per group by invoking the generic script module
module addUserScripts 'script.bicep' = [
  for (group, i) in dstsGroups: {
    name: '${databaseName}-addUserScript-${i}'
    params: {
      kustoName: kustoName
      databaseName: databaseName
      scriptName: 'dstsgroup-${i}'
      scriptContent: '.add database ${databaseName} users ( @\'dstsgroup=${group.name}\' ) "${group.description}"'
      continueOnErrors: continueOnErrors
    }
  }
]

@description('Names of all user-add script resources created.')
output scriptResourceNames array = [for (group, i) in dstsGroups: '${kustoName}/${databaseName}/dstsgroup-${i}-script']
