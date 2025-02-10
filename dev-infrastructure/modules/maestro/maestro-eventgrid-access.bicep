/*
This module manages access to EventGrid for a maestro client, which
can be the server or a consumer.

- An MQTT client is registered within eventgrid. Depending on the certificate
  issuer, the certificate validation schema will be thumbprint based for
  self-signed certificates and DNS based for OneCertV2 Private certificates.
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

@description('The issuer of the certificate.')
param certificateIssuer string = 'Self'

@description('The thumbprint of the certificate that should get access. Dont use in production')
param certificateThumbprint string

@description('The subject alternative name of the certificate')
param certificateSAN string

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2023-12-15-preview' existing = {
  name: eventGridNamespaceName
}

// D O N ' T   U S E   T H U M B P R I N T   I N   P R O D U C T I O N
// eventgrid MQTT client trusting the certificate by thumbprint if
// Key Vault self-signed certificates are used. trusting self-signed certificates
// as CAs is not supported in EventGrid
resource mqttClient 'Microsoft.EventGrid/namespaces/clients@2023-12-15-preview' = if (certificateIssuer == 'Self') {
  name: clientName
  parent: eventGridNamespace
  properties: {
    authenticationName: certificateSAN
    attributes: {
      role: clientRole
      consumer_name: clientName
    }
    clientCertificateAuthentication: {
      allowedThumbprints: [
        certificateThumbprint
      ]
      validationScheme: 'ThumbprintMatch'
    }
    state: 'Enabled'
  }
}

// TODO - implement issuer CA registration with EventGrid + register the mqtt client with
// the DnsMatchesAuthenticationName authentication validation scheme
