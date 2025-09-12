@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Name of the cache rule')
param artifactSyncRuleName string

@description('Source repository path')
param sourceRepositoryPath string

@description('Target repository name')
param targetRepositoryName string

@description('Artifact sync status')
param artifactSyncStatus string = 'Active'

@description('KQL query for artifact sync scope filter')
param kqlQuery string = 'Tags'

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

resource artifactSyncRule 'Microsoft.ContainerRegistry/registries/cacheRules@2024-01-01-preview' = {
  name: artifactSyncRuleName
  parent: acrResource
  properties: {
    sourceRepository: sourceRepositoryPath
    targetRepository: targetRepositoryName
    artifactSyncStatus: artifactSyncStatus
    artifactSyncScopeFilterProperties: {
      type: 'KQL'
      query: kqlQuery
    }
  }
}
