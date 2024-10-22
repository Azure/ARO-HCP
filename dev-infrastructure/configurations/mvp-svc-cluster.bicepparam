using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.30.4'
param istioVersion = ['asm-1-21']
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param persist = true
param aksClusterName = take('aro-hcp-svc-cluster-${uniqueString('svc-cluster')}', 63)
param aksKeyVaultName = 'aks-kv-aro-hcp-dev-sc'
param disableLocalAuth = false
param deployFrontendCosmos = true

param deployCsInfra = false
param csPostgresServerName = 'cs-pg-aro-hcp-dev'
param clusterServicePostgresPrivate = false

param serviceKeyVaultName = 'aro-hcp-dev-svc-kv'
param serviceKeyVaultResourceGroup = 'global'
param serviceKeyVaultSoftDelete = true
param serviceKeyVaultPrivate = false

param acrPullResourceGroups = ['global']
param clustersServiceAcrResourceGroupNames = ['global']
param imageSyncAcrResourceGroupNames = ['global']

param oidcStorageAccountName = 'arohcpoidcdev'
param aroDevopsMsiId = '/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/global/providers/Microsoft.ManagedIdentity/userAssignedIdentities/aro-hcp-devops'

param baseDNSZoneName = 'hcp.osadev.cloud'
param regionalDNSSubdomain = 'westus3'

// These parameters are always overridden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
