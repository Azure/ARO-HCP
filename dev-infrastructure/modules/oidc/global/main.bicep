param subdomain string
param parentZoneName string
param frontDoorProfileName string
param frontDoorEndpointName string
param frontDoorSkuName string
param securityPolicyName string
param wafPolicyName string
param keyVaultName string
param keyVaultAdminSPObjId string
param oidcMsiName string

//
//   D N S
//

resource oidcZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: '${subdomain}.${parentZoneName}'
  location: 'global'
}

module oidcZoneDelegation '../../dns/zone-delegation.bicep' = {
  name: '${subdomain}-frontdoor-zone-deleg'
  params: {
    childZoneName: subdomain
    childZoneNameservers: oidcZone.properties.nameServers
    parentZoneName: parentZoneName
  }
}

//
//   A Z U R E   F R O N T   D O O R
//
module frontDoor 'frontdoor-instance.bicep' = {
  name: 'azure-front-door'
  params: {
    frontDoorProfileName: frontDoorProfileName
    frontDoorEndpointName: frontDoorEndpointName
    frontDoorSkuName: frontDoorSkuName
    securityPolicyName: securityPolicyName
    wafPolicyName: wafPolicyName
  }
}

//
//   K E Y   V A U L T
//
module keyVault 'frontdoor-keyvault.bicep' = if (!empty(keyVaultName)) {
  name: 'frontdoor-keyvault'
  params: {
    keyVaultName: keyVaultName
    frontDoorPrincipalId: frontDoor.outputs.frontDoorPrincipalId
    keyVaultAdminPrincipalId: keyVaultAdminSPObjId
  }
}

//
//   M S I
//
resource oidcMsi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: oidcMsiName
  location: resourceGroup().location
}
