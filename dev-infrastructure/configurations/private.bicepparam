using '../templates/aks-development.bicep'

param kubernetesVersion = '1.29.2'
param vnetAddressPrefix = '10.132.0.0/14'
param subnetPrefix = '10.132.8.0/21'
param podSubnetPrefix = '10.132.64.0/18'
param deployFrontendCosmos = true
param enablePrivateCluster = true
param createdByConfigTag = 'private'

// This parameter is always overriden in the Makefile
param currentUserId = ''
