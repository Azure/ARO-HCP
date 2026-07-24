targetScope = 'subscription'

// Creates the subscription-scoped Azure Policy definition + assignment that
// appends tags.createdAt on new resource groups. cleanup-sweeper rg-ordered
// discovery treats the createdAt tag as required for any delete rule (see
// docs/ci/cleanup.md), so the e2e test subscriptions need this policy for the
// sweeper to consider their resource groups. This is the declarative
// equivalent of tooling/cleanup-sweeper/scripts/create-createdat-policy-assignment.sh;
// the rule body below is kept in sync with that script's
// rg-createdat-policy-rule.json, and both use the same resource names so they
// converge on the same definition/assignment.

@description('Policy definition resource name (matches create-createdat-policy-assignment.sh default)')
param policyDefinitionName string = 'aro-rg-createdat-tag'

@description('Policy assignment resource name (matches create-createdat-policy-assignment.sh default)')
param policyAssignmentName string = 'aro-createdat-rg'

@description('Display name for the policy definition and assignment')
param displayName string = 'ARO-CreatedAt Tag'

resource createdAtPolicyDefinition 'Microsoft.Authorization/policyDefinitions@2021-06-01' = {
  name: policyDefinitionName
  properties: {
    policyType: 'Custom'
    mode: 'All'
    displayName: displayName
    description: 'Append tags.createdAt on resource groups when missing (append + utcNow). mode=All for rg-ordered cleanup-sweeper discovery.'
    policyRule: {
      if: {
        allOf: [
          {
            field: 'type'
            equals: 'Microsoft.Resources/subscriptions/resourceGroups'
          }
          {
            field: 'tags[\'createdAt\']'
            exists: 'false'
          }
        ]
      }
      then: {
        effect: 'append'
        details: [
          {
            field: 'tags[\'createdAt\']'
            value: '[utcNow()]'
          }
        ]
      }
    }
  }
}

resource createdAtPolicyAssignment 'Microsoft.Authorization/policyAssignments@2022-06-01' = {
  name: policyAssignmentName
  properties: {
    displayName: displayName
    policyDefinitionId: createdAtPolicyDefinition.id
    enforcementMode: 'Default'
  }
}
