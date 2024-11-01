using '../templates/svc-cluster.bicep'

param kubernetesVersion = '{{ .kubernetesVersion }}'
param istioVersion = {{ .istioVersion }}
param vnetAddressPrefix = '{{ .vnetAddressPrefix }}'
param subnetPrefix = '{{ .subnetPrefix }}'
param podSubnetPrefix = '{{ .podSubnetPrefix }}'
param aksClusterName = '{{ .aksName }}'
param aksKeyVaultName = '{{ .svcEtcdKVName }}'
param aksEtcdKVEnableSoftDelete = {{ .svcEtcdKVSoftDelete }}

param disableLocalAuth = {{ .frontendCosmosDBDisableLocalAuth }}
param deployFrontendCosmos = {{ .frontendCosmosDBDeploy }}
param rpCosmosDbName = '{{ .frontendCosmosDBName }}'

param maestroKeyVaultName = '{{ .maestroKeyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestroEventgridName }}'
param maestroCertDomain = '{{ .maestroCertDomain}}'
param maestroPostgresServerName = '{{ .maestroPostgresName }}'
param maestroPostgresServerVersion = '{{ .maestroPostgresServerVersion }}'
param maestroPostgresServerStorageSizeGB = {{ .maestroPostgresServerStorageSizeGB }}
param deployMaestroPostgres = {{ .maestroPostgresDeploy }}
param maestroPostgresPrivate = {{ .maestroPostgresPrivate }}

param deployCsInfra = {{ .clusterServicePostgresDeploy }}
param csPostgresServerName = '{{ .clusterServicePostgresName }}'
param clusterServicePostgresPrivate = {{ .clusterServicePostgresPrivate }}

param serviceKeyVaultName = '{{ .serviceKeyVaultName }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVaultRG }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVaultRegion }}'
param serviceKeyVaultSoftDelete = {{ .serviceKeyVaultSoftDelete }}
param serviceKeyVaultPrivate = {{ .serviceKeyVaultPrivate }}

param acrPullResourceGroups = ['{{ .serviceComponentAcrResourceGroups }}']
param imageSyncAcrResourceGroupNames = ['{{ .imageSyncAcrRG }}']
param clustersServiceAcrResourceGroupNames = ['{{ .clusterServiceAcrRG }}']

param oidcStorageAccountName = '{{ .oidcStorageAccountName }}'
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

param regionalDNSZoneName = '{{ .regionalDNSSubdomain}}.{{ .baseDnsZoneName }}'

param regionalResourceGroup = '{{ .regionRG }}'
