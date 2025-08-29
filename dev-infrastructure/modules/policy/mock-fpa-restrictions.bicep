targetScope = 'subscription'

@description('Application ID of the mock FPA service principal to restrict')
param mockFpaAppId string

@description('Environment name for resource naming')
param environment string

@description('Enable enforcement of the policy (set to false for testing)')
param enforcementEnabled bool = true

var enforcementMode = enforcementEnabled ? 'Default' : 'DoNotEnforce'

// Policy to deny dangerous resource operations by the mock FPA
resource denyDangerousOperationsPolicy 'Microsoft.Authorization/policyDefinitions@2021-06-01' = {
  name: 'deny-mock-fpa-dangerous-ops-${environment}'
  properties: {
    displayName: 'Deny Mock FPA Dangerous Operations'
    description: 'Prevents mock FPA from dangerous operations while allowing checkAccess API calls'
    policyType: 'Custom'
    mode: 'All'
    metadata: {
      category: 'Security'
      description: 'Restricts Contributor permissions for mock FPA to prevent dangerous operations'
    }
    parameters: {
      effect: {
        type: 'String'
        metadata: {
          displayName: 'Effect'
          description: 'Enable or disable the execution of the policy'
        }
        allowedValues: [
          'Deny'
          'Disabled'
        ]
        defaultValue: 'Deny'
      }
      mockFpaAppId: {
        type: 'String'
        metadata: {
          displayName: 'Mock FPA Application ID'
          description: 'The application ID of the mock FPA service principal to restrict'
        }
      }
    }
    policyRule: {
      if: {
        allOf: [
          // Check if the request is coming from the mock FPA
          {
            // Match on the app ID in the request context
            value: '[requestContext().identity.appid]'
            equals: '[parameters(\'mockFpaAppId\')]'
          }
          {
            // Block dangerous resource types and operations
            anyOf: [
              // Block role assignments and definitions (privilege escalation)
              {
                allOf: [
                  {
                    field: 'type'
                    in: [
                      'Microsoft.Authorization/roleAssignments'
                      'Microsoft.Authorization/roleDefinitions'
                      'Microsoft.Authorization/policyAssignments'
                      'Microsoft.Authorization/policyDefinitions'
                    ]
                  }
                ]
              }
              // Block critical infrastructure deletion
              {
                allOf: [
                  {
                    field: 'type'
                    in: [
                      'Microsoft.Resources/resourceGroups'
                      'Microsoft.KeyVault/vaults'
                      'Microsoft.Storage/storageAccounts'
                      'Microsoft.Network/virtualNetworks'
                      'Microsoft.Compute/virtualMachines'
                      'Microsoft.ContainerService/managedClusters'
                    ]
                  }
                  {
                    // Only block delete operations for these resources
                    source: 'action'
                    like: 'Microsoft.*/delete'
                  }
                ]
              }
              // Block creation of high-privilege resources
              {
                allOf: [
                  {
                    field: 'type'
                    equals: 'Microsoft.Compute/virtualMachines'
                  }
                  {
                    source: 'action'
                    like: 'Microsoft.Compute/virtualMachines/write'
                  }
                ]
              }
            ]
          }
        ]
      }
      then: {
        effect: '[parameters(\'effect\')]'
      }
    }
  }
}

// Policy assignment for dangerous operations
resource denyDangerousOperationsAssignment 'Microsoft.Authorization/policyAssignments@2022-06-01' = {
  name: 'deny-mock-fpa-dangerous-ops-${environment}'
  location: deployment().location
  properties: {
    displayName: 'Deny Mock FPA Dangerous Operations - ${environment}'
    description: 'Restricts mock FPA Contributor permissions to prevent dangerous operations'
    policyDefinitionId: denyDangerousOperationsPolicy.id
    enforcementMode: enforcementMode
    parameters: {
      effect: {
        value: 'Deny'
      }
      mockFpaAppId: {
        value: mockFpaAppId
      }
    }
  }
  identity: {
    type: 'SystemAssigned'
  }
}

// Policy to allow only required network operations
resource allowRequiredNetworkOpsPolicy 'Microsoft.Authorization/policyDefinitions@2021-06-01' = {
  name: 'allow-mock-fpa-required-network-ops-${environment}'
  properties: {
    displayName: 'Allow Mock FPA Required Network Operations'
    description: 'Permits only the specific network operations required by mock FPA for service association links'
    policyType: 'Custom'
    mode: 'All'
    metadata: {
      category: 'Network'
      description: 'Allows mock FPA to perform only necessary subnet service association link operations'
    }
    parameters: {
      effect: {
        type: 'String'
        metadata: {
          displayName: 'Effect'
          description: 'Enable or disable the execution of the policy'
        }
        allowedValues: [
          'Audit'
          'Deny'
          'Disabled'
        ]
        defaultValue: 'Audit'
      }
      mockFpaAppId: {
        type: 'String'
        metadata: {
          displayName: 'Mock FPA Principal ID'
          description: 'The principal ID of the mock FPA service principal'
        }
      }
    }
    policyRule: {
      if: {
        allOf: [
          // Match requests from mock FPA
          {
            value: '[requestContext().identity.appid]'
            equals: '[parameters(\'mockFpaAppId\')]'
          }
          // Target subnet operations that are NOT service association links
          {
            field: 'type'
            equals: 'Microsoft.Network/virtualNetworks/subnets'
          }
          {
            not: {
              anyOf: [
                {
                  source: 'action'
                  contains: 'serviceAssociationLinks'
                }
                {
                  field: 'name'
                  contains: 'serviceAssociationLinks'
                }
              ]
            }
          }
        ]
      }
      then: {
        effect: '[parameters(\'effect\')]'
      }
    }
  }
}

// Assignment for network operations policy (audit mode to monitor)
resource allowRequiredNetworkOpsAssignment 'Microsoft.Authorization/policyAssignments@2022-06-01' = {
  name: 'allow-mock-fpa-network-ops-${environment}'
  location: deployment().location
  properties: {
    displayName: 'Allow Mock FPA Required Network Operations - ${environment}'
    description: 'Monitors mock FPA network operations to ensure only required operations are performed'
    policyDefinitionId: allowRequiredNetworkOpsPolicy.id
    enforcementMode: enforcementMode
    parameters: {
      effect: {
        value: 'Audit'
      }
      mockFpaAppId: {
        value: mockFpaAppId
      }
    }
  }
  identity: {
    type: 'SystemAssigned'
  }
}

@description('Policy definition IDs for reference')
output policyDefinitionIds array = [
  denyDangerousOperationsPolicy.id
  allowRequiredNetworkOpsPolicy.id
]

@description('Policy assignment IDs for reference')
output policyAssignmentIds array = [
  denyDangerousOperationsAssignment.id
  allowRequiredNetworkOpsAssignment.id
]

@description('Policy assignment names for easier management')
output policyAssignmentNames array = [
  denyDangerousOperationsAssignment.name
  allowRequiredNetworkOpsAssignment.name
]
