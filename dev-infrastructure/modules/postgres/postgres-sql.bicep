/*
Executes an SQL script on a PostgreSQL server using a user-assigned managed identity.
*/

@description('The postgres server name where an SQL script will be executed')
param postgresServerName string

@description('The database name where an SQL script will be executed')
param databaseName string

@description('The name of the user-assigned managed identity that will be used to execute the SQL script')
param postgresAdminManagedIdentityName string

@description('The SQL script to execute on the PostgreSQL server')
param sqlScript string

param forceUpdateTag string = guid('${sqlScript}/${postgresServerName}/${databaseName}+${postgresAdminManagedIdentityName}')

resource postgresAdminManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: postgresAdminManagedIdentityName
}

resource deploymentScript 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: deployment().name
  location: resourceGroup().location
  kind: 'AzureCLI'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${postgresAdminManagedIdentity.id}': {}
    }
  }

  properties: {
    azCliVersion: '2.30.0'
    cleanupPreference: 'OnSuccess'
    retentionInterval: 'P1D'
    scriptContent: '''
      az login --identity
      export PGPASSWORD=$(az account get-access-token --resource-type oss-rdbms | jq .accessToken -r)
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
        value: postgresAdminManagedIdentity.name
      }
    ]
    timeout: 'PT30M'
  }
}
