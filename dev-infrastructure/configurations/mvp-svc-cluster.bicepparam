using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.2'
param istioVersion = 'asm-1-20'
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param enablePrivateCluster = false
param persist = true
param aksKeyVaultName = 'aks-kv-aro-hcp-dev-sc'
param disableLocalAuth = false
param deployFrontendCosmos = true
param deployMaestroInfra = true
param maestroNamespace = 'maestro'
param maestroKeyVaultName = 'maestro-kv-aro-hcp-dev'
param maestroEventGridNamespacesName = 'maestro-eventgrid-aro-hcp-dev'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-dev.azure.com'
param maxClientSessionsPerAuthName = 2
param deployCsInfra = false
param csNamespace = 'cluster-service'
param csPostgresServerName = 'cs-pg-aro-hcp-dev'
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
    uamiName: 'cluster-service'
    namespace: csNamespace
    serviceAccountName: 'cluster-service'
  }
})
// This parameter is always overriden in the Makefile
param currentUserId = ''
param maestroInfraResourceGroup = ''
