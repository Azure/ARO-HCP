param name string
param aksClusterName string
param location string
param aksManagedIdentityId string
param manifests array

var namespaces = [for manifest in manifests: manifest.metadata.namespace]
var uniqueNamespaces = union(namespaces, [])
var namespaceManifests = [
  for i in range(0, length(uniqueNamespaces)): {
    apiVersion: 'v1'
    kind: 'Namespace'
    metadata: {
      name: uniqueNamespaces[i]
    }
  }
]
var namespaceManifestList = {
  apiVersion: 'v1'
  kind: 'List'
  items: namespaceManifests
}

var mainfestList = {
  apiVersion: 'v1'
  kind: 'List'
  items: manifests
}

resource deploymentScript 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: name
  location: location
  kind: 'AzureCLI'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${aksManagedIdentityId}': {}
    }
  }

  properties: {
    azCliVersion: '2.30.0'
    cleanupPreference: 'OnSuccess'
    retentionInterval: 'P1D'
    scriptContent: '''
      az login --identity
      az aks install-cli
      az aks get-credentials --resource-group ${AKS_CLUSTER_RG} --name ${AKS_CLUSTER_NAME} --overwrite-existing -a
      echo "${NAMESPACE_MANIFESTS}" | base64 -d | kubectl apply -f -
      echo "${MANIFESTS}" | base64 -d | kubectl apply -f -
    '''
    // todo figure out how to leverage az aks command invoke to
    // * avoid installing kubectl
    // * avoid the need for a network path to the cluster
    //
    // right now az aks command invoke fails with `MissingAADClusterToken` when run within a deploymentscript
    environmentVariables: [
      {
        name: 'AKS_CLUSTER_RG'
        value: resourceGroup().name
      }
      {
        name: 'AKS_CLUSTER_NAME'
        value: aksClusterName
      }
      {
        name: 'NAMESPACE_MANIFESTS'
        value: base64(string(namespaceManifestList))
      }
      {
        name: 'MANIFESTS'
        value: base64(string(mainfestList))
      }
    ]
    timeout: 'PT30M'
  }
}
