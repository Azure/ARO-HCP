using '../templates/mgmt-cluster.bicep'

param kubernetesVersion = '1.29.2'
param istioVersion = 'asm-1-20'
param vnetAddressPrefix = '10.132.0.0/14'
param subnetPrefix = '10.132.8.0/21'
param podSubnetPrefix = '10.132.64.0/18'
param enablePrivateCluster = false
param persist = false
param workloadIdentities = []

// This parameter is always overriden in the Makefile
param currentUserId = ''
