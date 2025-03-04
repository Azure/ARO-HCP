param location string = resourceGroup().location

@description('Name of the automation account')
param automationAccountName string

@description('Name of the managed identity')
param automationAccountManagedIdentity string = 'automation-account-identity'

param python3Packages array

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: automationAccountManagedIdentity
  location: location
}

resource automationAccount 'Microsoft.Automation/automationAccounts@2022-08-08' = {
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${uami.id}': {}
    }
  }
  name: automationAccountName
  location: location
  properties: {
    sku: {
      name: 'Basic'
    }
    publicNetworkAccess: false
  }
}

resource python3Package 'Microsoft.Automation/automationAccounts/python3Packages@2023-11-01' = [
  for pkg in python3Packages: {
    parent: automationAccount
    name: pkg.name
    properties: {
      contentLink: {
        contentHash: {
          algorithm: pkg.algorithm
          value: pkg.hash
        }
        uri: pkg.url
      }
    }
  }
]

output automationAccountManagedIdentityId string = uami.properties.principalId
