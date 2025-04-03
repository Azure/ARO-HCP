param gblRgName string
param gblSubscription string
param location string
param zoneName string
param frontDoorProfileName string
param storageAccountName string
param customDomainName string
param routeName string
param originGroupName string
param originName string
param privateLinkLocation string
param skuName string
param keyVaultName string
param deploymentScriptLocation string
param storageAccountBlobPublicAccess bool
param aroDevopsMsiId string
param storageAccountAccessPrincipalId string

var certificateName = 'afd-oic-${location}'
var requestMessage = 'Requested by OIDC pipeline'
var zoneNameReplacedDots = replace(zoneName, '\\.', '\\\\.')
var zoneNameReplacedHyphensDots = replace(zoneNameReplacedDots, '\\-', '\\\\-')

// Storage deployment
module storageAccount 'storage.bicep' = {
  name: 'storage'
  params: {
    accountName: storageAccountName
    location: location
    principalId: storageAccountAccessPrincipalId
    skuName: skuName
    deploymentMsiId: aroDevopsMsiId
    deploymentScriptLocation: deploymentScriptLocation
    allowBlobPublicAccess: storageAccountBlobPublicAccess
  }
}

// Custom Domain, Route, Origin deployment
module configureFrontDoor 'customDomain-route-origin.bicep' = {
  name: 'configureFrontDoor-${location}'
  params: {
    frontDoorProfileName: frontDoorProfileName
    frontDoorEndpointName: frontDoorProfileName
    zoneName: zoneName
    customDomainName: customDomainName
    routeName: routeName
    originGroupName: originGroupName
    originName: originName
    privateLinkLocation: privateLinkLocation
    storageName: storageAccount.outputs.storageName
    storageResourceGroup: resourceGroup().name
    storageSubscription: subscription().subscriptionId
    requestMessage: requestMessage
    keyVaultName: keyVaultName
    certificateName: certificateName
  }
  scope: resourceGroup(gblSubscription, gblRgName)
}

module StorageEndpoint 'privateConnectionEndpoint.bicep' = {
  name: 'storage-endpoint'
  params: {
    storageName: storageAccount.outputs.storageName
    requestMessage: requestMessage
  }
  dependsOn: [
    configureFrontDoor
  ]
}

// WAF deployment
module waf 'WAF.bicep' = {
  name: 'waf-policy-${location}'
  params: {
    frontDoorProfileName: frontDoorProfileName
    securityPolicyName: frontDoorProfileName
    oidcUrlWafPolicyName: frontDoorProfileName
    customDomainName: customDomainName
    discoveryDocRequestUriRegex: '^https:\\/\\/[a-z0-9\\-]+\\.${zoneNameReplacedHyphensDots}\\/[a-f0-9\\-]+\\/[a-f0-9\\-]+\\/\\.well-known\\/openid\\-configuration$'
    jwksRequestUriRegex: '^https:\\/\\/[a-z0-9\\-]+\\.${zoneNameReplacedHyphensDots}\\/[a-f0-9\\-]+\\/[a-f0-9\\-]+\\/openid\\/v[0-9]\\/jwks$'
  }
  scope: resourceGroup(gblSubscription, gblRgName)
  dependsOn: [
    configureFrontDoor
  ]
}
