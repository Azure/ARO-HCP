/*
Executes an SQL script on a PostgreSQL server using a user-assigned managed identity.
*/

@description('The postgres server name where an SQL script will be executed')
param postgresServerName string

@description('The database name where an SQL script will be executed')
param databaseName string

@description('The resource ID of the user-assigned managed identity that will be used to execute the SQL script')
param postgresAdministrationManagedIdentityId string

@description('The SQL script to execute on the PostgreSQL server')
param sqlScript string

param forceUpdateTag string = guid('${sqlScript}/${postgresServerName}/${databaseName}/${postgresAdministrationManagedIdentityId}')

import * as res from '../resource.bicep'

resource deploymentScript 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: deployment().name
  location: resourceGroup().location
  kind: 'AzureCLI'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${postgresAdministrationManagedIdentityId}': {}
    }
  }

  properties: {
    azCliVersion: '2.30.0'
    cleanupPreference: 'OnSuccess'
    retentionInterval: 'P1D'
    scriptContent: '''
      az login --identity
      export PGPASSWORD=$(az account get-access-token --resource-type oss-rdbms -o json | jq .accessToken -r)
      echo "${SQL_SCRIPT}" | base64 -d > script.sql
      apk add postgresql-client
      psql -f script.sql
    '''
    forceUpdateTag: forceUpdateTag
    environmentVariables: [
      {
        name: 'SQL_SCRIPT'
        value: base64(string(sqlScript))
      }
      {
        name: 'PGHOST'
        value: postgresServerName
      }
      {
        name: 'PGDATABASE'
        value: databaseName
      }
      {
        name: 'PGUSER'
        value: res.msiRefFromId(postgresAdministrationManagedIdentityId).name
      }
    ]
    timeout: 'PT30M'
  }
}
