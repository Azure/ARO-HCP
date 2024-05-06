using '../templates/aks-development.bicep'

param kubernetesVersion = '1.29.2'
param enablePrivateCluster = false
param deployFrontendCosmos = false
param createdByConfigTag = 'public'

// This parameter is always overriden in the Makefile
param currentUserId = ''
