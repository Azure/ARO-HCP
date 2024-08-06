using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.5'
param istioVersion = ['asm-1-20']
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param enablePrivateCluster = false
param persist = false
param aksClusterName = 'aro-hcp-svc-cluster'
param aksKeyVaultName = take('aks-kv-svc-cluster-${uniqueString(currentUserId)}', 24)
param aksEtcdKVEnableSoftDelete = false
param disableLocalAuth = false
param deployFrontendCosmos = false

param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = take('maestro-eg-${uniqueString(currentUserId)}', 24)
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-int.azure.com'
param maestroPostgresServerName = take('maestro-pg-${uniqueString(currentUserId)}', 60)
param maestroPostgresServerVersion = '15'
param maestroPostgresServerStorageSizeGB = 32
param deployMaestroPostgres = false

param deployCsInfra = false
param csPostgresServerName = take('cs-pg-${uniqueString(currentUserId)}', 60)

param serviceKeyVaultName = take('service-kv-${uniqueString(currentUserId)}', 24)
param serviceKeyVaultSoftDelete = false
param serviceKeyVaultPrivate = false

param workloadIdentities = items({
  frontend_wi: {
    uamiName: 'frontend'
    namespace: 'aro-hcp'
    serviceAccountName: 'frontend'
  }
  maestro_wi: {
    uamiName: 'maestro-server'
    namespace: 'maestro'
    serviceAccountName: 'maestro'
  }
  cs_wi: {
    uamiName: 'clusters-service'
    namespace: 'cluster-service'
    serviceAccountName: 'clusters-service'
  }
  image_sync_wi: {
    uamiName: 'image-sync'
    namespace: 'image-sync'
    serviceAccountName: 'image-sync'
  }
})

param acrPullResourceGroups = [regionalResourceGroup, 'global']
param imageSyncAcrResourceGroupNames = [regionalResourceGroup, 'global']
param clustersServiceAcrResourceGroupNames = [regionalResourceGroup, 'global']
 
// This parameter is always overriden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
