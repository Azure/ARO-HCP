using '../templates/mgmt-cluster.bicep'

param kubernetesVersion = '1.29.2'
param istioVersion = 'asm-1-20'
param vnetAddressPrefix = '10.132.0.0/14'
param subnetPrefix = '10.132.8.0/21'
param podSubnetPrefix = '10.132.64.0/18'
param enablePrivateCluster = false
param aksClusterName = 'aro-hcp-mgmt-cluster'
param aksKeyVaultName = take('aks-kv-mgmt-cluster-${uniqueString(currentUserId)}', 24)
param persist = false
param deployMaestroConsumer = false
param maestroNamespace = 'maestro'
param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = '${maestroInfraResourceGroup}-eventgrid'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-int.azure.com'

param workloadIdentities = items({
  maestro_wi: {
    uamiName: 'maestro-consumer'
    namespace: maestroNamespace
    serviceAccountName: 'maestro'
  }
})

// This parameter is always overriden in the Makefile
param currentUserId = ''
param maestroInfraResourceGroup = ''
