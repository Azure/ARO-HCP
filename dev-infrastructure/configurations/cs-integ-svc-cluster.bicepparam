using '../templates/svc-cluster.bicep'

param kubernetesVersion = '1.30.4'
param istioVersion = ['asm-1-22']
param vnetAddressPrefix = '10.128.0.0/14'
param subnetPrefix = '10.128.8.0/21'
param podSubnetPrefix = '10.128.64.0/18'
param persist = true
param aksClusterName = take('cs-integ-svc-cluster-${uniqueString('svc-cluster')}', 63)
param aksKeyVaultName = 'aks-kv-cs-integ-sc'
param disableLocalAuth = false
param deployFrontendCosmos = true

param maestroKeyVaultName = 'maestro-kv-cs-integ'
param maestroEventGridNamespacesName = 'maestro-eventgrid-cs-integ'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-dev.azure.com'
param maestroPostgresServerName = 'maestro-pg-cs-integ'
param maestroPostgresServerVersion = '15'
param maestroPostgresServerStorageSizeGB = 32
param deployMaestroPostgres = false
param maestroPostgresPrivate = false

param deployCsInfra = false
param csPostgresServerName = 'cs-pg-cs-integ'
param clusterServicePostgresPrivate = false

param serviceKeyVaultName = 'aro-hcp-dev-svc-kv'
param serviceKeyVaultResourceGroup = 'global'
param serviceKeyVaultSoftDelete = true
param serviceKeyVaultPrivate = false

param acrPullResourceGroups = ['global']
param clustersServiceAcrResourceGroupNames = ['global']
param imageSyncAcrResourceGroupNames = ['global']

param oidcStorageAccountName = 'arohcpoidccsinteg'
param aroDevopsMsiId = '/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/global/providers/Microsoft.ManagedIdentity/userAssignedIdentities/aro-hcp-devops'

param baseDNSZoneName = 'hcp.osadev.cloud'
param regionalDNSSubdomain = 'westus3-cs'

// These parameters are always overridden in the Makefile
param currentUserId = ''
param regionalResourceGroup = ''
