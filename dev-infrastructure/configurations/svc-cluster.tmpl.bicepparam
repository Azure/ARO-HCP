using '../templates/svc-cluster.bicep'

param kubernetesVersion = '{{ .kubernetesVersion }}'
param istioVersion = {{ .istioVersion }}
param vnetAddressPrefix = '{{ .vnetAddressPrefix }}'
param subnetPrefix = '{{ .subnetPrefix }}'
param podSubnetPrefix = '{{ .podSubnetPrefix }}'
param aksClusterName = '{{ .aksName }}'
param aksKeyVaultName = '{{ .svc.etcd.kvName }}'
param aksEtcdKVEnableSoftDelete = {{ .svc.etcd.kvSoftDelete }}

param disableLocalAuth = {{ .frontend.cosmosDB.disableLocalAuth }}
param deployFrontendCosmos = {{ .frontend.cosmosDB.deploy }}
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'

param maestroKeyVaultName = '{{ .maestro.keyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventgridName }}'
param maestroCertDomain = '{{ .maestro.certDomain}}'
param maestroPostgresServerName = '{{ .maestro.postgres.name }}'
param maestroPostgresServerVersion = '{{ .maestro.postgres.serverVersion }}'
param maestroPostgresServerStorageSizeGB = {{ .maestro.postgres.serverStorageSizeGB }}
param deployMaestroPostgres = {{ .maestro.postgres.deploy }}
param maestroPostgresPrivate = {{ .maestro.postgres.private }}

param deployCsInfra = {{ .clusterService.postgres.deploy }}
param csPostgresServerName = '{{ .clusterService.postgres.name }}'
param clusterServicePostgresPrivate = {{ .clusterService.postgres.private }}

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVault.region }}'
param serviceKeyVaultSoftDelete = {{ .serviceKeyVault.softDelete }}
param serviceKeyVaultPrivate = {{ .serviceKeyVault.private }}

param acrPullResourceGroups = ['{{ .serviceComponentAcrResourceGroups }}']
param imageSyncAcrResourceGroupNames = ['{{ .imageSync.acrRG }}']
param clustersServiceAcrResourceGroupNames = ['{{ .clusterService.acrRG }}']

param oidcStorageAccountName = '{{ .oidcStorageAccountName }}'
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

param regionalDNSZoneName = '{{ .regionalDNSSubdomain}}.{{ .baseDnsZoneName }}'

param regionalResourceGroup = '{{ .regionRG }}'
