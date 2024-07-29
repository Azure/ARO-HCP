using '../templates/mgmt-cluster.bicep'

param kubernetesVersion = '1.29.5'
param vnetAddressPrefix = '10.132.0.0/14'
param subnetPrefix = '10.132.8.0/21'
param podSubnetPrefix = '10.132.64.0/18'
param enablePrivateCluster = false
param aksClusterName = take('aro-hcp-mgmt-cluster-${uniqueString('mgmt-cluster')}', 63)
param aksKeyVaultName = 'aks-kv-aro-hcp-dev-mc-1'
param persist = true
param deployMaestroConsumer = true
param maestroKeyVaultName = 'maestro-kv-aro-hcp-dev'
param maestroEventGridNamespacesName = 'maestro-eventgrid-aro-hcp-dev'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-dev.azure.com'

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

param acrPullResourceGroups = [regionalResourceGroup, 'global']

// These parameters are always overridden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
