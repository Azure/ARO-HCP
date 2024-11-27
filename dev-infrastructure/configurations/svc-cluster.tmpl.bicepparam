using '../templates/svc-cluster.bicep'

param kubernetesVersion = '{{ .kubernetesVersion }}'
param istioVersion = ['{{ .istioVersion }}']
param vnetAddressPrefix = '{{ .vnetAddressPrefix }}'
param subnetPrefix = '{{ .subnetPrefix }}'
param podSubnetPrefix = '{{ .podSubnetPrefix }}'
param aksClusterName = '{{ .aksName }}'
param aksKeyVaultName = '{{ .svc.etcd.kvName }}'
param aksEtcdKVEnableSoftDelete = any('{{ .svc.etcd.kvSoftDelete }}')

param userAgentMinCount = any('{{ .svc.userAgentPool.minCount }}')
param userAgentMaxCount = any('{{ .svc.userAgentPool.maxCount }}')
param userAgentVMSize = '{{ .svc.userAgentPool.vmSize }}'
param aksUserOsDiskSizeGB = any({{ .svc.userAgentPool.osDiskSizeGB }})
param userAgentPoolAZCount = any('{{ .svc.userAgentPool.azCount }}')

param disableLocalAuth = any('{{ .frontend.cosmosDB.disableLocalAuth }}')
param deployFrontendCosmos = any('{{ .frontend.cosmosDB.deploy }}')
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param rpCosmosDbPrivate = any('{{ .frontend.cosmosDB.private }}')

param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroServerMqttClientName = '{{ .maestro.serverMqttClientName }}'
param maestroCertDomain = '{{ .maestro.certDomain}}'
param maestroPostgresServerName = '{{ .maestro.postgres.name }}'
param maestroPostgresServerMinTLSVersion = '{{ .maestro.postgres.minTLSVersion }}'
param maestroPostgresServerVersion = '{{ .maestro.postgres.serverVersion }}'
param maestroPostgresServerStorageSizeGB = any('{{ .maestro.postgres.serverStorageSizeGB }}')
param deployMaestroPostgres = any('{{ .maestro.postgres.deploy }}')
param maestroPostgresPrivate = any('{{ .maestro.postgres.private }}')

param deployCsInfra = any('{{ .clusterService.postgres.deploy }}')
param csPostgresServerName = '{{ .clusterService.postgres.name }}'
param csPostgresServerMinTLSVersion = '{{ .clusterService.postgres.minTLSVersion }}'
param clusterServicePostgresPrivate = any('{{ .clusterService.postgres.private }}')

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVault.region }}'
param serviceKeyVaultSoftDelete = any('{{ .serviceKeyVault.softDelete }}')
param serviceKeyVaultPrivate = any('{{ .serviceKeyVault.private }}')

param acrPullResourceGroups = ['{{ .serviceComponentAcrResourceGroups }}']
param clustersServiceAcrResourceGroupNames = ['{{ .clusterService.acrRG }}']

param oidcStorageAccountName = '{{ .oidcStorageAccountName }}'
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

param regionalDNSZoneName = '{{ .regionalDNSSubdomain}}.{{ .baseDnsZoneName }}'

param regionalResourceGroup = '{{ .regionRG }}'
