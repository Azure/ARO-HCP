/*
This module creates the infrastructure required by maestro to run. This includes:

- Create an EventGrid namespaces instance with MQTT enabled.
- Create EventGrid client groups for the server and consumers and define topic
  access permissions.

Execution scope: the resourcegroup of the maestro infrastructure
*/

@description('The Maestro Event Grid Namespaces name')
param eventGridNamespaceName string

@description('The location of the EventGrid Namespace')
param location string

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maxClientSessionsPerAuthName int

@description('Allow public network access to the EventGrid Namespace')
@allowed([
  'Enabled'
  'Disabled'
])
param publicNetworkAccess string

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
    publicNetworkAccess: publicNetworkAccess
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
