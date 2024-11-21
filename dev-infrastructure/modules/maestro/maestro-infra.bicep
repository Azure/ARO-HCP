/*
This module creates the infrastructure required by maestro to run. This includes:

- A KeyVault where the client certificates for EventGrid MQTT broker access
  are generated and stored
- A managed identity to create and manage certificates in Key Vault. This is
  used by the maestro-eventgrid-access bicep module deploymentscripts.

    Why is this needed? There are no bicep modules for KeyVault certificate management,
    so we need deploymentscripts + a managed identity with Key Vault access to run them.

- Create an EventGrid namespaces instance with MQTT enabled.
- Create EventGrid client groups for the server and consumers and define topic
  access permissions.

Execution scope: the resourcegroup of the maestro infrastructure

TODO:
- Key Vault network access restrictions (e.g. privatelink)
- EventGrid network access restrictions (e.g. privatelink)
*/

@description('The Maestro Event Grid Namespaces name')
param eventGridNamespaceName string

@description('The location of the EventGrid Namespace')
param location string

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maxClientSessionsPerAuthName int

@description('The name for the Key Vault for Maestro certificates')
param maestroKeyVaultName string

@description('The name for the Managed Identity that will be created for Key Vault Certificate management.')
param kvCertOfficerManagedIdentityName string

@description('Minimum TLS version allowed for the EventGrid Namespace')
param minimumTlsVersionAllowed string = '1.2'

@description('Allow public network access to the EventGrid Namespace')
@allowed([
  'Enabled'
  'Disabled'
])
param publicNetworkAccess string = 'Enabled'

//
//   K E Y    V A U L T
//

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: maestroKeyVaultName
  location: location
  tags: {
    resourceGroup: resourceGroup().name
  }
  properties: {
    accessPolicies: []
    enableRbacAuthorization: true
    enabledForDeployment: false
    enabledForDiskEncryption: false
    enabledForTemplateDeployment: false
    enableSoftDelete: false
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Allow'
      ipRules: [
        {
          // TODO: restrict in higher environments
          value: '0.0.0.0/0'
        }
      ]
    }
    // TODO: disabled in higher environments
    publicNetworkAccess: 'Enabled'
    sku: {
      family: 'A'
      name: 'standard'
    }
    tenantId: subscription().tenantId
  }
}

//
// C E R T I F I C A T E   O F F I C E R   M S I
//

resource kvCertOfficerManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: kvCertOfficerManagedIdentityName
  location: location
}

var keyVaultCertificateOfficerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'a4417e6f-fecd-4de8-b567-7b0420556985'
)

resource kvManagedIdentityRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: kv
  name: guid(kvCertOfficerManagedIdentity.id, keyVaultCertificateOfficerRoleId, kv.id)
  properties: {
    roleDefinitionId: keyVaultCertificateOfficerRoleId
    principalId: kvCertOfficerManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

//
//   E V E N T   G R I D
//

// create an event grid namespace with MQTT enabled
resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2024-06-01-preview' = {
  name: eventGridNamespaceName
  location: location
  sku: {
    name: 'Standard'
    capacity: 1
  }
  properties: {
    isZoneRedundant: true
    publicNetworkAccess: publicNetworkAccess
    minimumTlsVersionAllowed: minimumTlsVersionAllowed
    topicSpacesConfiguration: {
      state: 'Enabled'
      maximumSessionExpiryInHours: 1
      maximumClientSessionsPerAuthenticationName: maxClientSessionsPerAuthName
      clientAuthentication: {
        alternativeAuthenticationNameSources: [
          'ClientCertificateDns'
        ]
      }
    }
  }
}

//
//   E V E N T   G R I D   M A E S T R O   S E R V E R   C O N F I G
//

// an MQTT client group to hold the maestro server client
resource maestroServerMqttClientGroup 'Microsoft.EventGrid/namespaces/clientGroups@2023-12-15-preview' = {
  name: 'maestro-server'
  parent: eventGridNamespace
  properties: {
    query: 'attributes.role IN [\'server\']'
  }
}

// create a topic space for the maestro server to subscribe to
resource maestroServerSubscribeTopicspace 'Microsoft.EventGrid/namespaces/topicSpaces@2023-12-15-preview' = {
  name: 'maestro-server-subscribe'
  parent: eventGridNamespace
  properties: {
    topicTemplates: [
      'sources/maestro/consumers/+/agentevents'
    ]
  }
}

// ... and grant the maestro server client permission to subscribe to the topic space
resource maestroServerPermissionBindingSubscribe 'Microsoft.EventGrid/namespaces/permissionBindings@2023-12-15-preview' = {
  name: 'maestro-server-subscribe-binding'
  parent: eventGridNamespace
  properties: {
    clientGroupName: maestroServerMqttClientGroup.name
    permission: 'Subscriber'
    topicSpaceName: maestroServerSubscribeTopicspace.name
  }
}

// create a topic space for the maestro server to publish to
resource maestroServerPublishTopicspace 'Microsoft.EventGrid/namespaces/topicSpaces@2023-12-15-preview' = {
  name: 'maestro-server-publish'
  parent: eventGridNamespace
  properties: {
    topicTemplates: [
      'sources/maestro/consumers/+/sourceevents'
    ]
  }
  dependsOn: [
    maestroServerSubscribeTopicspace // this dependency prevents concurrent topicspace updates
  ]
}

// ... and grant the maestro server client permission to publish to the topic space
resource maestroServerPermissionBindingPublish 'Microsoft.EventGrid/namespaces/permissionBindings@2023-12-15-preview' = {
  name: 'maestro-server-publish-binding'
  parent: eventGridNamespace
  properties: {
    clientGroupName: maestroServerMqttClientGroup.name
    permission: 'Publisher'
    topicSpaceName: maestroServerPublishTopicspace.name
  }
}

//
//   E V E N T   G R I D   M A E S T R O   C O N S U M E R  C O N F I G
//

// an MQTT client group to hold the maestro consumer clients
resource maestroConsumerMqttClientGroup 'Microsoft.EventGrid/namespaces/clientGroups@2023-12-15-preview' = {
  name: 'maestro-consumers'
  parent: eventGridNamespace
  properties: {
    query: 'attributes.role IN [\'consumer\']'
  }
}

// create a topic space for the maestro consumers to subscribe to
resource maestroConsumersSubscribeTopicspace 'Microsoft.EventGrid/namespaces/topicSpaces@2023-12-15-preview' = {
  name: 'maestro-consumer-subscribe'
  parent: eventGridNamespace
  properties: {
    topicTemplates: [
      'sources/maestro/consumers/\${client.attributes.consumer_name}/sourceevents'
    ]
  }
  dependsOn: [
    maestroServerPublishTopicspace // this dependency prevents concurrent topicspace updates
  ]
}

// ... and grant the maestro consumer client group permission to subscribe to the topic space
resource maestroConsumersSubscribeTopicspacePermissionBinding 'Microsoft.EventGrid/namespaces/permissionBindings@2023-12-15-preview' = {
  name: 'maestro-consumer-subscribe'
  parent: eventGridNamespace
  properties: {
    clientGroupName: maestroConsumerMqttClientGroup.name
    permission: 'Subscriber'
    topicSpaceName: maestroConsumersSubscribeTopicspace.name
  }
}

// create a topic space for the maestro consumers to publish to
resource maestroConsumersPublishTopicspace 'Microsoft.EventGrid/namespaces/topicSpaces@2023-12-15-preview' = {
  name: 'maestro-consumer-publish'
  parent: eventGridNamespace
  properties: {
    topicTemplates: [
      'sources/maestro/consumers/\${client.attributes.consumer_name}/agentevents'
    ]
  }
  dependsOn: [
    maestroConsumersSubscribeTopicspace // this dependency prevents concurrent topicspace updates
  ]
}

// ... and grant the maestro consumer client group permission to publish to the topic space
resource maestroConsumersPublishTopicspacePermissionBinding 'Microsoft.EventGrid/namespaces/permissionBindings@2023-12-15-preview' = {
  name: 'maestro-consumer-publish'
  parent: eventGridNamespace
  properties: {
    clientGroupName: maestroConsumerMqttClientGroup.name
    permission: 'Publisher'
    topicSpaceName: maestroConsumersPublishTopicspace.name
  }
}

output keyVaultName string = kv.name
output eventGridNamespaceName string = eventGridNamespace.name
