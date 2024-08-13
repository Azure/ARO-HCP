using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.7'
param istioVersion = ['asm-1-20']
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
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

param acrPullResourceGroups = ['global']
param imageSyncAcrResourceGroupNames = ['global']
param clustersServiceAcrResourceGroupNames = ['global']

// These parameters are always overriden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
param azureMonitorWorkspaceResourceId = ''
