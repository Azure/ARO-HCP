using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.5'
param istioVersion = ['asm-1-20']
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param enablePrivateCluster = false
param persist = true
param aksClusterName = take('aro-hcp-svc-cluster-${uniqueString('svc-cluster')}', 63)
param aksKeyVaultName = 'aks-kv-aro-hcp-dev-sc'
param disableLocalAuth = false
param deployFrontendCosmos = true

param maestroKeyVaultName = 'maestro-kv-aro-hcp-dev'
param maestroEventGridNamespacesName = 'maestro-eventgrid-aro-hcp-dev'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-dev.azure.com'
param maestroPostgresServerName = 'maestro-pg-aro-hcp-dev'
param maestroPostgresServerVersion = '15'
param maestroPostgresServerStorageSizeGB = 32
param deployMaestroPostgres = false

param deployCsInfra = false
param csPostgresServerName = 'cs-pg-aro-hcp-dev'

param serviceKeyVaultName = 'service-kv-aro-hcp-dev'
param serviceKeyVaultSoftDelete = true
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

param acrPullResourceGroups = ['global']
param clustersServiceAcrResourceGroupNames = ['global']

// These parameters are always overridden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
param azureMonitorWorkspaceResourceId = ''
