/*
This module deploys an SecretProviderClass CR for CSI secret store into
a namespace of an AKS cluster. The secret references provided in `objects`
match the `spec.parameters.objects.array` specification of the Azure Key Vault
provider for Secret Store CSI driver. See https://azure.github.io/secrets-store-csi-driver-provider-azure/docs/getting-started/usage/
for more details.

Execution scope: the resourcegroup of the AKS cluster
*/

@description('The name of the key vault where the secrets are stored')
param keyVaultName string

@description('The client id of the managed identitity used to access the key vault')
param clientId string

@description('The name of the secret SecretProviderClass CR to be created')
param csiSecProviderClassName string

@description('The namespace where the SecretProviderClass CR will be created')
param namespace string

@description('The name of the AKS cluster where the SecretProviderClass CR will be created')
param aksClusterName string

type keyVaultSecretType = 'secret' | 'key' | 'cert'
type csiSecretRefType = {
  objectName: string
  objectType: keyVaultSecretType
  objectAlias: string?
}

@description('The secrets that will be accessible when the SecretProviderClass CR is mounted into a Pod')
param objects csiSecretRefType[]

param location string

var objectsYamlList = [
  for obj in objects: '  - |\n    objectName: ${obj.objectName}\n    objectType: ${obj.objectType}\n    objectAlias: ${obj.objectAlias ?? '""'}'
]
var objectsYaml = 'array:\n${join(objectsYamlList, '\n')}'

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-01-01' existing = {
  name: aksClusterName
}

module maestroCSISecretStoreConfig '../modules/aks-manifest.bicep' = {
  name: '${deployment().name}-${csiSecProviderClassName}-manifest'
  params: {
    aksClusterName: aksClusterName
    manifests: [
      {
        apiVersion: 'secrets-store.csi.x-k8s.io/v1'
        kind: 'SecretProviderClass'
        metadata: {
          name: csiSecProviderClassName
          namespace: namespace
        }
        spec: {
          provider: 'azure'
          parameters: {
            usePodIdentity: 'false'
            clientID: clientId
            tenantId: subscription().tenantId
            keyvaultName: keyVaultName
            // todo generalize this
            cloudName: 'AzurePublicCloud'
            objects: objectsYaml
          }
        }
      }
    ]
    aksManagedIdentityId: items(aksCluster.identity.userAssignedIdentities)[0].key
    location: location
  }
}
