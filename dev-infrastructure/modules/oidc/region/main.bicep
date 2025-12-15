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
param useManagedCertificates bool
param deploymentScriptLocation string
param storageAccountBlobPublicAccess bool
param globalMSIId string
param storageAccountAccessPrincipalIds array
param frontDoorManage bool

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
    principalIds: storageAccountAccessPrincipalIds
    skuName: skuName
    deploymentMsiId: globalMSIId
    deploymentScriptLocation: deploymentScriptLocation
    allowBlobPublicAccess: storageAccountBlobPublicAccess
  }
}

// Custom Domain, Route, Origin deployment
module configureFrontDoor 'customDomain-route-origin.bicep' = if (frontDoorManage) {
  name: 'configureFrontDoor-${location}'
  scope: resourceGroup(gblSubscription, gblRgName)
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
    useManagedCertificates: useManagedCertificates
    certificateName: certificateName
  }
}

module StorageEndpoint 'privateConnectionEndpoint.bicep' = if (frontDoorManage) {
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
module waf 'WAF.bicep' = if (frontDoorManage) {
  name: 'waf-policy-${location}'
  scope: resourceGroup(gblSubscription, gblRgName)
  params: {
    frontDoorProfileName: frontDoorProfileName
    securityPolicyName: storageAccountName
    oidcUrlWafPolicyName: storageAccountName
    customDomainName: customDomainName
    discoveryDocRequestUriRegex: '^https:\\/\\/[a-z0-9\\-]+\\.${zoneNameReplacedHyphensDots}\\/[a-f0-9\\-]+\\/[a-z0-9]+\\/\\.well-known\\/openid\\-configuration$'
    jwksRequestUriRegex: '^https:\\/\\/[a-z0-9\\-]+\\.${zoneNameReplacedHyphensDots}\\/[a-f0-9\\-]+\\/[a-z0-9]+\\/openid\\/v[0-9]\\/jwks$'
  }
  dependsOn: [
    configureFrontDoor
  ]
}
