param cosmosDBAccountName string
param location string
param userAssignedMIs array
param readOnlyUserAssignedMIs array = []

param resourceContainerMaxScale int
param billingContainerMaxScale int
param locksContainerMaxScale int
param fleetContainerMaxScale int

var containers = [
  {
    name: 'Resources'
    defaultTtl: -1 // On, no default expiration
    partitionKeyPaths: ['/partitionKey']
    maxThroughput: resourceContainerMaxScale
  }
  {
    name: 'Billing'
    defaultTtl: -1 // On, no default expiration
    partitionKeyPaths: ['/subscriptionId']
    maxThroughput: billingContainerMaxScale
  }
  {
    name: 'Locks'
    defaultTtl: 10
    partitionKeyPaths: ['/id']
    maxThroughput: locksContainerMaxScale
  }
  {
    name: 'Fleet'
    defaultTtl: -1 // On, no default expiration
    partitionKeyPaths: ['/partitionKey']
    maxThroughput: fleetContainerMaxScale
  }
]

param roleDefinitionId string = '00000000-0000-0000-0000-000000000002'
param readOnlyRoleDefinitionId string = '00000000-0000-0000-0000-000000000001'

resource cosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  name: cosmosDBAccountName
}

resource cosmosDb 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2023-11-15' existing = {
  name: cosmosDBAccountName
  parent: cosmosDbAccount
}

resource cosmosDbContainers 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2023-11-15' = [
  for c in containers: {
    parent: cosmosDb
    name: c.name
    properties: {
      options: {
        autoscaleSettings: {
          maxThroughput: c.maxThroughput
        }
      }
      resource: {
        id: c.name
        defaultTtl: c.defaultTtl
        indexingPolicy: {
          indexingMode: 'consistent'
          automatic: true
          includedPaths: [
            {
              path: '/*'
            }
          ]
          excludedPaths: [
            {
              path: '/"_etag"/?'
            }
          ]
        }
        partitionKey: {
          paths: c.partitionKeyPaths
          kind: 'Hash'
          version: 2
        }
        uniqueKeyPolicy: {
          uniqueKeys: []
        }
        conflictResolutionPolicy: {
          mode: 'LastWriterWins'
          conflictResolutionPath: '/_ts'
        }
        computedProperties: []
      }
    }
  }
]

// Wait for all UAMI principals to be visible in Entra before creating role assignments.
// Freshly created managed identities may not be immediately resolvable by CosmosDB,
// causing "principal ID was not found in the AAD tenant" errors.
var allPrincipalIds = [for uami in concat(userAssignedMIs, readOnlyUserAssignedMIs): uami.uamiPrincipalID]

resource waitForPrincipals 'Microsoft.Resources/deploymentScripts@2023-08-01' = if (length(allPrincipalIds) > 0) {
  name: 'wait-for-entra-principals'
  location: location
  kind: 'AzureCLI'
  properties: {
    azCliVersion: '2.63.0'
    retentionInterval: 'PT1H'
    timeout: 'PT10M'
    forceUpdateTag: guid(join(allPrincipalIds, ','))
    environmentVariables: [
      {
        name: 'PRINCIPAL_IDS'
        value: join(allPrincipalIds, ',')
      }
    ]
    scriptContent: '''
      #!/bin/bash
      set -e
      IFS=',' read -ra PRINCIPALS <<< "$PRINCIPAL_IDS"
      for pid in "${PRINCIPALS[@]}"; do
        echo "Waiting for principal $pid to be visible in Entra..."
        for i in $(seq 1 18); do
          if az ad sp show --id "$pid" &>/dev/null; then
            echo "  Principal $pid found (attempt $i)"
            break
          fi
          if [ $i -eq 18 ]; then
            echo "  WARNING: Principal $pid not found after 18 attempts (3 min), proceeding anyway"
          fi
          sleep 10
        done
      done
      echo "All principals checked."
    '''
  }
}

resource sqlRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = [
  for uami in userAssignedMIs: {
    name: guid(roleDefinitionId, uami.uamiPrincipalID, cosmosDbAccount.id)
    parent: cosmosDbAccount
    properties: {
      roleDefinitionId: '/${subscription().id}/resourceGroups/${resourceGroup().name}/providers/Microsoft.DocumentDB/databaseAccounts/${cosmosDbAccount.name}/sqlRoleDefinitions/${roleDefinitionId}'
      principalId: uami.uamiPrincipalID
      scope: cosmosDbAccount.id
    }
    dependsOn: [waitForPrincipals]
  }
]

resource sqlRoleAssignmentReadOnly 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-15' = [
  for uami in readOnlyUserAssignedMIs: {
    name: guid(readOnlyRoleDefinitionId, uami.uamiPrincipalID, cosmosDbAccount.id)
    parent: cosmosDbAccount
    properties: {
      roleDefinitionId: '/${subscription().id}/resourceGroups/${resourceGroup().name}/providers/Microsoft.DocumentDB/databaseAccounts/${cosmosDbAccount.name}/sqlRoleDefinitions/${readOnlyRoleDefinitionId}'
      principalId: uami.uamiPrincipalID
      scope: cosmosDbAccount.id
    }
    dependsOn: [waitForPrincipals]
  }
]
