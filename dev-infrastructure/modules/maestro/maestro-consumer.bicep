param aksClusterName string
param maestroServerManagedIdentityPrincipalId string
param maestroServerManagedIdentityClientId string
param namespace string

@minLength(1)
param maestroConsumerName string
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
    clientName: maestroConsumerName
    clientRole: 'consumer'
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
  sourceEvents: sources/maestro/consumers/{1}/sourceevents
  agentEvents: sources/maestro/consumers/{1}/agentevents
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
          'config.yaml': format(configMap, evengGridAccess.outputs.EventGridHostname, maestroConsumerName)
          'ca.crt': loadTextContent('../../scripts/digicert-global-root-g3.crt')
          'consumer-name.cfg': maestroConsumerName
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
  name: '${deployment().name}-csi-secret-store'
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
