/*
This module manages access to EventGrid for a maestro client, which
can be the server or a consumer.

- An MQTT client is registered within eventgrid. The certificate validation
  schema uses DNS based validation, matching the certificate SAN against the
  authentication name. This way certificates can be rotated without updating
  the eventgrid MQTT client configuration.
- The MQTT client is placed into the right MQTT client group based on the
  client role. This defines the topic access permissions for the client.

Execution scope: the resourcegroup of the eventgrid namespace instance
*/

@description('The EventGrid Namespace name where access will be managed')
param eventGridNamespaceName string

@description('The name of the client that will be created in the EventGrid Namespace')
param clientName string

@description('The role of the client in the EventGrid Namespace.')
@allowed([
  'server'
  'consumer'
])
param clientRole string

@description('The subject alternative name of the certificate')
param certificateSAN string

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2023-12-15-preview' existing = {
  name: eventGridNamespaceName
}

resource certMqttClient 'Microsoft.EventGrid/namespaces/clients@2023-12-15-preview' = {
  name: clientName
  parent: eventGridNamespace
  properties: {
    authenticationName: certificateSAN
    attributes: {
      role: clientRole
      consumer_name: clientName
    }
    clientCertificateAuthentication: {
      validationScheme: 'DnsMatchesAuthenticationName'
    }
    state: 'Enabled'
  }
}
