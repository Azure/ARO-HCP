/*
This module is responsible for:
 - setting up EventGrid access for the maestro server
 - placeing MQTT broker configuration into the maestro namespace of an AKS cluster
 - placeing CSI secret store configuration into the maestro namespace of an AKS cluster

Execution scope: the resourcegroup of the AKS cluster where the maestro server
will be deployed.

TODO:
- once Key Vault and EventGrid have network access restrictions enabled,
  this module needs to be enhanced to manage access to both (e.g. privatelink)
*/

param aksClusterName string
param maestroServerManagedIdentityPrincipalId string
param maestroServerManagedIdentityClientId string
param namespace string

param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string
param maestroKeyVaultName string
param maestroKeyVaultOfficerManagedIdentityName string
param maestroKeyVaultCertificateDomain string

param location string

module evengGridAccess './maestro-eventgrid-access.bicep' = {
  name: '${deployment().name}-event-grid-access'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespaceName
    keyVaultName: maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroKeyVaultOfficerManagedIdentityName
    certDomain: maestroKeyVaultCertificateDomain
    clientName: 'maestro-server'
    clientRole: 'server'
    certificateAccessManagedIdentityPrincipalId: maestroServerManagedIdentityPrincipalId
    location: location
  }
}

// Maestro MQTT K8S Secret

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-01-01' existing = {
  name: aksClusterName
}

var configMap = '''
brokerHost: "{0}:8883"
username: ""
password: ""
caFile: /secrets/mqtt/ca.crt
clientCertFile: /secrets/mqtt-creds/maestro.crt
clientKeyFile: /secrets/mqtt-creds/maestro.key
topics:
  sourceEvents: sources/maestro/consumers/+/sourceevents
  agentEvents: sources/maestro/consumers/+/agentevents
'''

module maestroConfigMap '../aks-manifest.bicep' = {
  name: '${deployment().name}-mqtt-secret-manifest'
  params: {
    aksClusterName: aksCluster.name
    manifests: [
      {
        apiVersion: 'v1'
        kind: 'Secret'
        metadata: {
          name: 'maestro-mqtt'
          namespace: 'maestro'
        }
        stringData: {
          'config.yaml': format(configMap, evengGridAccess.outputs.EventGridHostname)
          'ca.crt': loadTextContent('../../scripts/digicert-global-root-g3.crt')
        }
      }
    ]
    aksManagedIdentityId: items(aksCluster.identity.userAssignedIdentities)[0].key
    location: location
  }
  dependsOn: [
    evengGridAccess
  ]
}

// maestro CSI secret store configuration to access the EventGrid
// access certificates stored in KeyVault

module maestroCSISecretStoreConfig '../aks-csi-secret-store.bicep' = {
  name: '${deployment().name}-csi-secret-store-manifest'
  params: {
    aksClusterName: aksClusterName
    clientId: maestroServerManagedIdentityClientId
    keyVaultName: maestroKeyVaultName
    location: location
    namespace: namespace
    csiSecProviderClassName: 'maestro'
    objects: [
      {
        objectName: evengGridAccess.outputs.KeyVaultCertName
        objectType: 'secret'
        objectAlias: 'maestro'
      }
    ]
  }
  dependsOn: [
    evengGridAccess
  ]
}
