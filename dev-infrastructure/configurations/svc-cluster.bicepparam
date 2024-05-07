using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.29.2'
param vnetAddressPrefix = enablePrivateCluster ? '10.132.0.0/14' : '10.128.0.0/14'
param subnetPrefix = enablePrivateCluster ? '10.132.8.0/21' : '10.128.8.0/21'
param podSubnetPrefix = enablePrivateCluster ? '10.132.64.0/18' : '10.128.64.0/18'
param enablePrivateCluster = false
param persist = false
param disableLocalAuth = false
param deployFrontendCosmos = false

// This parameter is always overriden in the Makefile
param currentUserId = ''
