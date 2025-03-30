@minLength(5)
@maxLength(80)
@description('Name of the Azure Public IP address')
param name string

@description('Location of the Public IP address')
param location string

@description('List of Availability Zones for the Public IP address')
param zones array = []

@description('IPTags for the Public IP address')
param ipTags array = []

@description('Tags to set on the Public IP address')
param tags object = {}

@description('The Public IP address\'s role assignment properties')
param roleAssignmentProperties object = {}

resource publicIPAddress 'Microsoft.Network/publicIPAddresses@2024-05-01' = {
  name: name
  location: location
  tags: tags
  properties: {
    ipTags: ipTags
    publicIPAddressVersion: 'IPv4'
    publicIPAllocationMethod: 'Static'
  }
  sku: {
    name: 'Standard'
    tier: 'Regional'
  }
  zones: zones
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (roleAssignmentProperties != {}) {
  name: guid(publicIPAddress.id, roleAssignmentProperties.principalId, roleAssignmentProperties.roleDefinitionId)
  properties: roleAssignmentProperties
  scope: publicIPAddress
}

output ipAddress string = publicIPAddress.properties.ipAddress
output resourceId string = publicIPAddress.id
