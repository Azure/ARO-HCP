using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.2'
param istioVersion = 'asm-1-20'
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param enablePrivateCluster = false
param persist = false
param disableLocalAuth = false
param deployFrontendCosmos = false
param deployMaestroInfra = false
param maestroNamespace = 'maestro'
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
})
// This parameter is always overriden in the Makefile
param currentUserId = ''
