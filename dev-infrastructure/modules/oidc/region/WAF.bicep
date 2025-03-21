@description('The name of the front door SKU')
param frontDoorSkuName string = 'Premium_AzureFrontDoor'

@description('The name of the front door profile')
param frontDoorProfileName string

@description('The name of the security policy on front door profile')
param securityPolicyName string

@description('The name of the WAF on front door profile to allow valid oidc urls')
param oidcUrlWafPolicyName string

param customDomainName string

// https://learn.microsoft.com/en-us/azure/web-application-firewall/afds/waf-front-door-policy-configure-bot-protection?pivots=bicep
@description('The list of managed rule sets to configure on the WAF.')
param wafManagedRuleSets array = [
  {
    ruleSetType: 'Microsoft_BotManagerRuleSet'
    ruleSetVersion: '1.0'
  }
]

@allowed([
  'Detection'
  'Prevention'
])
@description('The mode that the WAF should be deployed using. In \'Prevention\' mode, the WAF will block requests it detects as malicious. In \'Detection\' mode, the WAF will not block requests and will simply log the request.')
param wafMode string = 'Prevention'

@description('The regEx pattren to match valid oidc discovery doc request uri.')
param discoveryDocRequestUriRegex string

@description('The regEx pattren to match valid oidc JWKS key request uri.')
param jwksRequestUriRegex string

// Read the Readme.md for more information on the regex patterns and explanation.
@description('The list of custom rule sets to configure on the WAF to ensure valid oidc urls.')
param oidcWafCustomRuleSets array = [
  {
    name: 'AllowedOIDCUrls'
    priority: 100
    enabledState: 'Enabled'
    ruleType: 'MatchRule'
    action: 'Allow'
    matchConditions: [
      {
        matchVariable: 'RequestUri'
        operator: 'RegEx'
        negateCondition: false
        matchValue: [
          discoveryDocRequestUriRegex
        ]
      }
      {
        matchVariable: 'RequestUri'
        operator: 'RegEx'
        negateCondition: false
        matchValue: [
          jwksRequestUriRegex
        ]
      }
    ]
  }
  {
    name: 'BlockNonOidcUrls'
    priority: 200
    enabledState: 'Enabled'
    ruleType: 'MatchRule'
    action: 'Block'
    matchConditions: [
      {
        matchVariable: 'RequestUri'
        operator: 'RegEx'
        negateCondition: true
        matchValue: [
          discoveryDocRequestUriRegex
        ]
      }
      {
        matchVariable: 'RequestUri'
        operator: 'RegEx'
        negateCondition: true
        matchValue: [
          jwksRequestUriRegex
        ]
      }
    ]
  }
  {
    name: 'RateLimitOnIPs'
    enabledState: 'Enabled'
    priority: 300
    ruleType: 'RateLimitRule'
    rateLimitDurationInMinutes: 1
    rateLimitThreshold: 500
    matchConditions: [
      {
        matchVariable: 'RemoteAddr'
        operator: 'IPMatch'
        negateCondition: false
        matchValue: [
          '0.0.0.0/0'
        ]
        transforms: []
      }
    ]
    action: 'Log'
  }
]

resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' existing = {
  name: frontDoorProfileName
}

resource oidcUrlWafPolicy 'Microsoft.Network/FrontDoorWebApplicationFirewallPolicies@2022-05-01' = {
  name: oidcUrlWafPolicyName
  location: 'global'
  sku: {
    name: frontDoorSkuName
  }
  properties: {
    policySettings: {
      enabledState: 'Enabled'
      mode: wafMode
      customBlockResponseStatusCode: 403
      customBlockResponseBody: base64('Unintended URL, access is not allowed')
      requestBodyCheck: 'Enabled'
    }
    customRules: {
      rules: oidcWafCustomRuleSets
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
        id: oidcUrlWafPolicy.id
      }
      associations: [
        {
          domains: [
            {
              id: resourceId('Microsoft.Cdn/profiles/customdomains', frontDoorProfileName, customDomainName)
            }
          ]
          patternsToMatch: [
            '/*'
          ]
        }
      ]
    }
  }
  dependsOn: [
    frontDoorProfile
    oidcUrlWafPolicy
  ]
}
