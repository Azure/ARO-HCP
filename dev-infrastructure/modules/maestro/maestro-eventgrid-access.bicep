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
param certificateIssuer string

@description('The thumbprint of the certificate that should get access. Dont use in production')
param certificateThumbprint string

@description('The subject alternative name of the certificate')
param certificateSAN string

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2023-12-15-preview' existing = {
  name: eventGridNamespaceName
}

//
//   T H U M B P R I N T   V A L I D A T I O N   S C H E M E
//   D O N ' T   U S E  I N   P R O D U C T I O N
//
// With this scheme, eventgrid MQTT trusts client certificates by thumbprint. For self-signed
// certificates, this is the only secure option. But it comes at the price of updating the
// thumbprints in the eventgrid namespace every time a new certificate is issued.
//
resource selfSignedCertMqttClient 'Microsoft.EventGrid/namespaces/clients@2023-12-15-preview' = if (certificateIssuer == 'Self') {
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

//
//   D N S   V A L I D A T I O N   S C H E M E
//
// This is the scheme we want to use for production. It allows us to trust CA signed
// certificates by validating the certificate SAN against the authentication name.
// This way certificates can be rotated without updating the eventgrid MQTT client
// configuration.
//
resource certMqttClient 'Microsoft.EventGrid/namespaces/clients@2023-12-15-preview' = if (certificateIssuer != 'Self') {
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
      validationScheme: 'DnsMatchesAuthenticationName'
    }
    state: 'Enabled'
  }
}
