/*
Manages access to a postgres database by creating a new user and granting access to a database.
The user will also be enabled for entra authentication.
*/

@description('The name of the postgres server that will be managed')
param postgresServerName string

@description('The name of the managed identity that will be used to manage access in the database')
param postgresAdminManagedIdentityName string

@description('The principal ID / object ID of the managed identity that will be granted access to')
param newUserPrincipalId string

@description('The name of the managed identity that will be granted access to')
param newUserName string

@description('The name of the database, the new new user will be granted access to')
param databaseName string

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-03-01-preview' existing = {
  name: postgresServerName
}

var sqlScriptLines = [
  'do'
  '$$'
  'begin'
  '  if not exists (select * from pg_user where usename = \'${newUserName}\') then'
  '    create user "${newUserName}";'
  '  end if;'
  'end'
  '$$'
  ';'
  'SECURITY LABEL for "pgaadauth" on role "${newUserName}" is \'aadauth,oid=${newUserPrincipalId},type=service\';'
  'GRANT ALL PRIVILEGES ON DATABASE ${databaseName} TO "${newUserName}";'
]

module csManagedIdentityDatabaseAccess 'postgres-sql.bicep' = {
  name: '${deployment().name}-db-access'
  params: {
    postgresServerName: postgres.properties.fullyQualifiedDomainName
    databaseName: 'postgres' // access configuration is managed in the postgres DB
    postgresAdminManagedIdentityName: postgresAdminManagedIdentityName
    sqlScript: string(join(sqlScriptLines, '\n'))
  }
}
