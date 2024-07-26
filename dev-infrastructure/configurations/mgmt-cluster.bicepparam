using '../templates/mgmt-cluster.bicep'

param kubernetesVersion = '1.29.5'
param vnetAddressPrefix = '10.132.0.0/14'
param subnetPrefix = '10.132.8.0/21'
param podSubnetPrefix = '10.132.64.0/18'
param enablePrivateCluster = false
param aksClusterName = 'aro-hcp-mgmt-cluster'
param additionalAcrResourceGroups = ['aro-hcp-dev']
param aksKeyVaultName = take('aks-kv-mgmt-cluster-${uniqueString(currentUserId)}', 24)
param aksEtcdKVEnableSoftDelete = false
param persist = false
param deployMaestroConsumer = false
param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = take('maestro-eg-${uniqueString(currentUserId)}', 24)
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-int.azure.com'

param baseDNSZoneName = 'hcp.osadev.cloud'

param workloadIdentities = items({
  maestro_wi: {
    uamiName: 'maestro-consumer'
    namespace: 'maestro'
    serviceAccountName: 'maestro'
  }
  external_dns_wi: {
    uamiName: 'external-dns'
    namespace: 'hypershift'
    serviceAccountName: 'external-dns'
  }
})

// This parameter is always overriden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
