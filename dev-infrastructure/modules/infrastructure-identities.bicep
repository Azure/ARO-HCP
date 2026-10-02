targetScope = 'subscription'

type identityBundle = {
  @minLength(1)
  resourceGroupName: string
  @minLength(1)
  identityNames: string[]
}

type managementIdentityBundle = {
  @minLength(1)
  stampIdentifier: string
  @minLength(1)
  resourceGroupName: string
  @minLength(1)
  identityNames: string[]
}

param location string
param serviceBundle identityBundle
@minLength(1)
param managementBundles managementIdentityBundle[]

var bundles = concat([serviceBundle], managementBundles)

resource identityResourceGroups 'Microsoft.Resources/resourceGroups@2024-03-01' = [
  for bundle in bundles: {
    name: bundle.resourceGroupName
    location: location
    tags: {
      purpose: 'leased-infrastructure-identities'
      persist: 'true'
    }
  }
]

module managedIdentities 'managed-identities.bicep' = [
  for (bundle, i) in bundles: {
    name: 'infrastructure-identities-${uniqueString(bundle.resourceGroupName)}'
    scope: identityResourceGroups[i]
    params: {
      location: location
      manageIdentityNames: bundle.identityNames
    }
  }
]

output infrastructureIdentityBundle object = {
  serviceResourceGroup: serviceBundle.resourceGroupName
  managementResourceGroups: toObject(
    managementBundles,
    bundle => bundle.stampIdentifier,
    bundle => bundle.resourceGroupName
  )
}
