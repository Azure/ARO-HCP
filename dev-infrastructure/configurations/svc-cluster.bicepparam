using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.2'
param istioVersion = 'asm-1-20'
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param enablePrivateCluster = false
param persist = false
param aksClusterName = 'aro-hcp-svc-cluster'
param additionalAcrResourceGroups = ['aro-hcp-dev']
param aksKeyVaultName = take('aks-kv-svc-cluster-${uniqueString(currentUserId)}', 24)
param aksEtcdKVEnableSoftDelete = false
param disableLocalAuth = false
param deployFrontendCosmos = false

param deployMaestroInfra = false
param maestroNamespace = 'maestro'
param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = '${maestroInfraResourceGroup}-eventgrid'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-int.azure.com'
param maxClientSessionsPerAuthName = 2
param maestroPostgresServerName = take('maestro-pg-${uniqueString(currentUserId)}', 60)
param maestroPostgresServerVersion = '15'
param maestroPostgresServerStorageSizeGB = 32

param deployCsInfra = true
param csNamespace = 'cluster-service'
param csPostgresServerName = take('cs-pg-${uniqueString(currentUserId)}', 60)

param serviceKeyVaultName = take('service-kv-${uniqueString(currentUserId)}', 24)
param serviceKeyVaultSoftDelete = false
param serviceKeyVaultPrivate = false

param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'
param regionalDNSSubdomain = 'reg-${take(uniqueString(currentUserId), 5)}'

param workloadIdentities = items({
  frontend_wi: {
    uamiName: 'frontend'
    namespace: 'aro-hcp'
    serviceAccountName: 'frontend'
  }
  maestro_wi: {
    uamiName: 'maestro-server'
    namespace: maestroNamespace
    serviceAccountName: 'maestro'
  }
  cs_wi: {
    uamiName: 'clusters-service'
    namespace: csNamespace
    serviceAccountName: 'clusters-service'
  }
})
// This parameter is always overriden in the Makefile
param currentUserId = ''
param maestroInfraResourceGroup = ''
