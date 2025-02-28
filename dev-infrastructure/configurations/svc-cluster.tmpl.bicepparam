using '../templates/svc-cluster.bicep'

param istioVersions = '{{ .svc.istio.versions }}'

// AKS
param kubernetesVersion = '{{ .svc.aks.kubernetesVersion }}'
param vnetAddressPrefix = '{{ .svc.aks.vnetAddressPrefix }}'
param subnetPrefix = '{{ .svc.aks.subnetPrefix }}'
param podSubnetPrefix = '{{ .svc.aks.podSubnetPrefix }}'
param istioIngressGatewayIPAddressName = '{{ .svc.istio.ingressGatewayIPAddressName }}'
param istioIngressGatewayIPAddressIPTags = '{{ .svc.istio.ingressGatewayIPAddressIPTags }}'
param aksClusterName = '{{ .svc.aks.name }}'
param aksKeyVaultName = '{{ .svc.aks.etcd.kvName }}'
param aksEtcdKVEnableSoftDelete = {{ .svc.aks.etcd.kvSoftDelete }}
param systemAgentMinCount = {{ .svc.aks.systemAgentPool.minCount}}
param systemAgentMaxCount = {{ .svc.aks.systemAgentPool.maxCount }}
param systemAgentVMSize = '{{ .svc.aks.systemAgentPool.vmSize }}'
param aksSystemOsDiskSizeGB = {{ .svc.aks.systemAgentPool.osDiskSizeGB }}
param userAgentMinCount = {{ .svc.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .svc.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .svc.aks.userAgentPool.vmSize }}'
param userAgentPoolAZCount = {{ .svc.aks.userAgentPool.azCount }}
param aksUserOsDiskSizeGB = {{ .svc.aks.userAgentPool.osDiskSizeGB }}
param aksClusterOutboundIPAddressIPTags = '{{ .svc.aks.clusterOutboundIPAddressIPTags }}'

param disableLocalAuth = {{ .frontend.cosmosDB.disableLocalAuth }}
param deployFrontendCosmos = {{ .frontend.cosmosDB.deploy }}
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param rpCosmosDbPrivate = {{ .frontend.cosmosDB.private }}
param rpCosmosZoneRedundantMode = '{{ .frontend.cosmosDB.zoneRedundantMode }}'

param maestroMIName = '{{ .maestro.server.managedIdentityName }}'
param maestroNamespace = '{{ .maestro.server.k8s.namespace }}'
param maestroServiceAccountName = '{{ .maestro.server.k8s.serviceAccountName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroServerMqttClientName = '{{ .maestro.server.mqttClientName }}'
param maestroCertDomain = '{{ .maestro.certDomain }}'
param maestroCertIssuer = '{{ .maestro.certIssuer }}'
param maestroPostgresServerName = '{{ .maestro.postgres.name }}'
param maestroPostgresServerMinTLSVersion = '{{ .maestro.postgres.minTLSVersion }}'
param maestroPostgresServerVersion = '{{ .maestro.postgres.serverVersion }}'
param maestroPostgresServerStorageSizeGB = {{ .maestro.postgres.serverStorageSizeGB }}
param maestroPostgresDatabaseName = '{{ .maestro.postgres.databaseName }}'
param deployMaestroPostgres = {{ .maestro.postgres.deploy }}
param maestroPostgresPrivate = {{ .maestro.postgres.private }}

param csPostgresDeploy = {{ .clusterService.postgres.deploy }}
param csPostgresServerName = '{{ .clusterService.postgres.name }}'
param csPostgresServerMinTLSVersion = '{{ .clusterService.postgres.minTLSVersion }}'
param clusterServicePostgresPrivate = {{ .clusterService.postgres.private }}
param csMIName = '{{ .clusterService.managedIdentityName }}'
param csNamespace = '{{ .clusterService.k8s.namespace }}'
param csServiceAccountName = '{{ .clusterService.k8s.serviceAccountName }}'

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'

// ACR Resource IDs
param ocpAcrResourceId = '__ocpAcrResourceId__'
param svcAcrResourceId = '__svcAcrResourceId__'

// OIDC
param oidcStorageAccountName = '{{ .oidcStorageAccountName }}'
param oidcZoneRedundantMode = '{{ .oidcZoneRedundantMode }}'

param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

param regionalCXDNSZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.cxParentZoneName }}'
param regionalSvcDNSZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.svcParentZoneName }}'

param regionalResourceGroup = '{{ .regionRG }}'

param frontendIngressCertName = '{{ .frontend.cert.name }}'
param frontendIngressCertIssuer = '{{ .frontend.cert.issuer }}'
param genevaActionsServiceTag = '{{ .genevaActions.serviceTag }}'

// Azure Monitor Workspace
param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'

// MDSD / Genevabits
@description('The namespace of the logs')
param logsNamespace = '{{ .logs.mdsd.namespace }}'
param logsMSI = '{{ .logs.mdsd.msiName }}'
param logsServiceAccount = '{{ .logs.mdsd.serviceAccountName }}'

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId = '__logAnalyticsWorkspaceId__'
