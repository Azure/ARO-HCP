using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.2'
param istioVersion = 'asm-1-20'
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param enablePrivateCluster = false
param persist = false
param aksKeyVaultName = take('aks-kv-svc-cluster-${uniqueString(currentUserId)}', 24)
param disableLocalAuth = false
param deployFrontendCosmos = false
param deployMaestroInfra = false
param maestroNamespace = 'maestro'
param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = '${maestroInfraResourceGroup}-eventgrid'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-int.azure.com'
param deployCsInfra = false
param csNamespace = 'cs'
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
    uamiName: 'cs'
    namespace: csNamespace
    serviceAccountName: 'cs'
  }
})
// This parameter is always overriden in the Makefile
param currentUserId = ''
param currentUserPrincipal = ''
param maestroInfraResourceGroup = ''
