@description('The name of the front door profile')
param frontDoorProfileName string

@description('The name of the front door SKU')
param frontDoorSkuName string

@description('The name of the front door endpoint')
param frontDoorEndpointName string

@description('The name of the security policy on front door profile')
param securityPolicyName string

@description('The name of the WAF on front door profile')
param wafPolicyName string

@allowed([
  'Detection'
  'Prevention'
])
@description('The mode that the WAF should be deployed using. In \'Prevention\' mode, the WAF will block requests it detects as malicious. In \'Detection\' mode, the WAF will not block requests and will simply log the request.')
param wafMode string = 'Detection'

@description('The list of managed rule sets to configure on the WAF.')
param wafManagedRuleSets array = [
  {
    ruleSetType: 'Microsoft_BotManagerRuleSet'
    ruleSetVersion: '1.0'
  }
]

@description('The list of custom rule sets to configure on the WAF.')
param wafCustomRuleSets array = [
  {
    name: 'RateLimit'
    priority: 100
    enabledState: 'Enabled'
    ruleType: 'RateLimitRule'
    rateLimitThreshold: 100
    rateLimitDurationInMinutes: 5
    action: 'Block'
    matchConditions: [
      {
        matchVariable: 'SocketAddr'
        operator: 'IPMatch'
        negateCondition: false
        matchValue: [
          '::/0'
        ]
      }
    ]
  }
]

resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' = {
  name: frontDoorProfileName
  location: 'global'
  sku: {
    name: frontDoorSkuName
  }
  identity: {
    type: 'SystemAssigned'
  }
}

resource frontDoorEndpoint 'Microsoft.Cdn/profiles/afdEndpoints@2023-05-01' = {
  name: frontDoorEndpointName
  parent: frontDoorProfile
  location: 'global'
  properties: {
    enabledState: 'Enabled'
  }
}

resource wafPolicy 'Microsoft.Network/FrontDoorWebApplicationFirewallPolicies@2024-02-01' = {
  name: wafPolicyName
  location: 'global'
  sku: {
    name: frontDoorSkuName
  }
  properties: {
    policySettings: {
      enabledState: 'Enabled'
      mode: wafMode
    }
    customRules: {
      rules: wafCustomRuleSets
    }
    managedRules: {
      managedRuleSets: wafManagedRuleSets
    }
  }
}

resource securityPolicy 'Microsoft.Cdn/profiles/securityPolicies@2023-05-01' = {
  parent: frontDoorProfile
  name: securityPolicyName
  properties: {
    parameters: {
      type: 'WebApplicationFirewall'
      wafPolicy: {
        id: wafPolicy.id
      }
      associations: [
        {
          domains: [
            {
              id: frontDoorEndpoint.id
            }
          ]
          patternsToMatch: [
            '/*'
          ]
        }
      ]
    }
  }
}

output frontDoorPrincipalId string = frontDoorProfile.identity.principalId
