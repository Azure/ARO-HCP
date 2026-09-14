@description('The resource ID of the Cosmos DB account for the RP')
param rpCosmosDbAccountId string

@description('The name of the kube-applier managed identity.')
param kubeApplierMIName string

@description('The CosmosDB container name for kube-applier.')
param kubeApplierContainerName string

@description('The autoscale max throughput for the kube-applier CosmosDB container.')
param kubeApplierContainerMaxScale int

@description('The principal ID of the Clusters Service (CS) managed identity, sourced from the service resource group. Granted read/write on the per-management-cluster kube-applier CosmosDB container.')
param csManagedIdentityPrincipalId string

import * as res from '../modules/resource.bicep'

var rpCosmosDbAccountRef = res.cosmosDBAccountRefFromId(rpCosmosDbAccountId)

resource kubeApplierMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: kubeApplierMIName
}

module kubeApplierCosmos '../modules/rp-cosmos-kube-applier.bicep' = if (rpCosmosDbAccountId != '') {
  name: 'kube-applier-cosmos-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(rpCosmosDbAccountRef.resourceGroup.subscriptionId, rpCosmosDbAccountRef.resourceGroup.name)
  params: {
    cosmosDBAccountName: rpCosmosDbAccountRef.name
    containerName: kubeApplierContainerName
    containerMaxScale: kubeApplierContainerMaxScale
    kubeApplierManagedIdentityPrincipalId: kubeApplierMSI.properties.principalId
    csManagedIdentityPrincipalId: csManagedIdentityPrincipalId
  }
}
