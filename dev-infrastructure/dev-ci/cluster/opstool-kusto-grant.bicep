@description('Name of the user-assigned identity to grant access to.')
param managedIdentityName string

@description('Name of the Kusto cluster holding the database.')
param kustoName string

@description('Resource group of the Kusto cluster.')
param kustoResourceGroup string

@description('Name of the database to grant access on.')
param databaseName string

// The collector running on this cluster records CI job outcomes in the dev
// Kusto, so that the alerts and logs already held there can be attributed to
// the run that produced them.
//
// It needs both roles. Ingestor lets it write rows, and Viewer lets it read
// back the newest row it already wrote, which is how each pass knows where to
// resume. Ingestor alone cannot read, so the collector would fail on its first
// query and never ingest anything.
//
// These are declared here rather than through modules/logs/kusto/grant-access
// because that module names every assignment 'grant-<guid(principal, database)>'
// regardless of role, so passing one principal as both an ingestor and a reader
// would produce two resources with the same name.
resource collectorIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: managedIdentityName
}

module grantCollector 'opstool-kusto-grant-assignments.bicep' = {
  name: 'grantKusto-${databaseName}-${uniqueString(managedIdentityName, databaseName)}'
  scope: resourceGroup(kustoResourceGroup)
  params: {
    principalId: collectorIdentity.properties.principalId
    kustoName: kustoName
    databaseName: databaseName
  }
}
