param location string

@description('Access mode for this NSP')
@allowed(['Audit', 'Enforced', 'Learning'])
param accessMode string

@description('Resource IDs to associate with this NSP')
param associatedResources array

@description('The Name the NSP should have')
param nspName string

@description('Array of IPs that will be allowd to access NSP')
param addressPrefixes array = []

@description('Array of Service Tags that will be allowd to access NSP')
param serviceTags array = []

@description('Array of Subscription ids that will be allowd to access NSP')
param subscriptions array = []

var subscriptionObjects = [for s in subscriptions: { 'id': s }]

resource nsp 'Microsoft.Network/networkSecurityPerimeters@2024-06-01-preview' = {
  location: location
  name: nspName
}

resource nspProfile 'Microsoft.Network/networkSecurityPerimeters/profiles@2024-06-01-preview' = {
  parent: nsp
  location: location
  name: '${nspName}-profile'
}

resource accessRule 'Microsoft.Network/networkSecurityPerimeters/profiles/accessRules@2024-06-01-preview' = if (length(addressPrefixes) > 0 || length(serviceTags) > 0 || length(subscriptionObjects) > 0) {
  parent: nspProfile
  location: location
  name: '${nspName}-inbound-accessRule'
  properties: {
    addressPrefixes: addressPrefixes
    direction: 'Inbound'
    serviceTags: serviceTags
    subscriptions: subscriptionObjects
  }
}

resource resourecAssociation 'Microsoft.Network/networkSecurityPerimeters/resourceAssociations@2024-06-01-preview' = [
  for ar in associatedResources: {
    parent: nsp
    location: location
    name: '${nspName}-ra-${guid(ar)}'
    properties: {
      accessMode: accessMode
      privateLinkResource: {
        id: ar
      }
      profile: {
        id: nspProfile.id
      }
    }
  }
]
