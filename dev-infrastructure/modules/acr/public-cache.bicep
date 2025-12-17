@description('Globally unique name of the Azure Container Registry')
param acrName string

type publicRepositoryCache = {
  @description('Name for the cache rule')
  ruleName: string

  @description('Source repository URL (e.g., "registry.k8s.io/ingress-nginx/*")')
  sourceRepo: string

  @description('Target repository name (e.g., "k8s-cache/ingress-nginx/*")')
  targetRepo: string
}

@description('Array of public repositories to cache (no authentication required)')
param publicRepositoriesToCache publicRepositoryCache[]

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

//
//   P U B L I C   C A C H E   R U L E S   ( N O   A U T H )
//

resource publicCacheRule 'Microsoft.ContainerRegistry/registries/cacheRules@2023-01-01-preview' = [
  for repo in publicRepositoriesToCache: {
    name: repo.ruleName
    parent: acrResource
    properties: {
      sourceRepository: repo.sourceRepo
      targetRepository: repo.targetRepo
    }
  }
]
