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
param aksKeyVaultName = '{{ .svc.aks.etcd.name }}'
param aksKeyVaultTagName = '{{ .svc.aks.etcd.tagKey }}'
param aksKeyVaultTagValue = '{{ .svc.aks.etcd.tagValue }}'
param aksEtcdKVEnableSoftDelete = {{ .svc.aks.etcd.softDelete }}
param systemAgentMinCount = {{ .svc.aks.systemAgentPool.minCount}}
param systemAgentMaxCount = {{ .svc.aks.systemAgentPool.maxCount }}
param systemAgentPoolCount = {{ .svc.aks.systemAgentPool.poolCount }}
param systemAgentPoolZones = '{{ .svc.aks.systemAgentPool.zones }}'
param systemAgentVMSize = '{{ .svc.aks.systemAgentPool.vmSize }}'
param systemZoneRedundantMode = '{{ .svc.aks.systemAgentPool.zoneRedundantMode }}'
param aksSystemOsDiskSizeGB = {{ .svc.aks.systemAgentPool.osDiskSizeGB }}
param userAgentMinCount = {{ .svc.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .svc.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .svc.aks.userAgentPool.vmSize }}'
param userAgentPoolCount = {{ .svc.aks.userAgentPool.poolCount }}
param userAgentPoolZones = '{{ .svc.aks.userAgentPool.zones }}'
param userZoneRedundantMode = '{{ .svc.aks.userAgentPool.zoneRedundantMode }}'
param infraAgentMinCount = {{ .svc.aks.infraAgentPool.minCount }}
param infraAgentMaxCount = {{ .svc.aks.infraAgentPool.maxCount }}
param infraAgentVMSize = '{{ .svc.aks.infraAgentPool.vmSize }}'
param infraAgentPoolCount = {{ .svc.aks.infraAgentPool.poolCount }}
param infraAgentPoolZones = '{{ .svc.aks.infraAgentPool.zones }}'
param infraZoneRedundantMode = '{{ .svc.aks.infraAgentPool.zoneRedundantMode }}'
param infraOsDiskSizeGB = {{ .svc.aks.infraAgentPool.osDiskSizeGB }}
param userOsDiskSizeGB = {{ .svc.aks.userAgentPool.osDiskSizeGB }}
param aksClusterOutboundIPAddressIPTags = '{{ .svc.aks.clusterOutboundIPAddressIPTags }}'
param aksNetworkDataplane = '{{ .svc.aks.networkDataplane }}'
param aksNetworkPolicy = '{{ .svc.aks.networkDataplane }}'

param disableLocalAuth = {{ .frontend.cosmosDB.disableLocalAuth }}
param deployFrontendCosmos = {{ .frontend.cosmosDB.deploy }}
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param rpCosmosDbPrivate = {{ .frontend.cosmosDB.private }}
param rpCosmosZoneRedundantMode = '{{ .frontend.cosmosDB.zoneRedundantMode }}'
param frontendMIName = '{{ .frontend.managedIdentityName }}'
param frontendNamespace = '{{ .frontend.k8s.namespace }}'
param frontendServiceAccountName = '{{ .frontend.k8s.serviceAccountName }}'
param backendMIName = '{{ .backend.managedIdentityName }}'
param backendNamespace = '{{ .backend.k8s.namespace }}'
param backendServiceAccountName = '{{ .backend.k8s.serviceAccountName }}'

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
param maestroPostgresZoneRedundantMode = '{{ .maestro.postgres.zoneRedundantMode }}'
param maestroPostgresBackupRetentionDays = {{ .maestro.postgres.backupRetentionDays }}
param maestroPostgresGeoRedundantBackup = {{ .maestro.postgres.geoRedundantBackup }}
param maestroPostgresPrivate = {{ .maestro.postgres.private }}

param csPostgresDeploy = {{ .clustersService.postgres.deploy }}
param csPostgresZoneRedundantMode = '{{ .clustersService.postgres.zoneRedundantMode }}'
param csPostgresBackupRetentionDays = {{ .clustersService.postgres.backupRetentionDays }}
param csPostgresGeoRedundantBackup = {{ .clustersService.postgres.geoRedundantBackup }}
param csPostgresServerName = '{{ .clustersService.postgres.name }}'
param csPostgresServerMinTLSVersion = '{{ .clustersService.postgres.minTLSVersion }}'
param csPostgresServerVersion = '{{ .clustersService.postgres.serverVersion }}'
param csPostgresServerStorageSizeGB = {{ .clustersService.postgres.serverStorageSizeGB }}
param csPostgresDatabaseName = '{{ .clustersService.postgres.databaseName }}'
param clusterServicePostgresPrivate = {{ .clustersService.postgres.private }}
param csMIName = '{{ .clustersService.managedIdentityName }}'
param csNamespace = '{{ .clustersService.k8s.namespace }}'
param csServiceAccountName = '{{ .clustersService.k8s.serviceAccountName }}'

param msiRefresherMIName = '{{ .msiCredentialsRefresher.managedIdentityName }}'
param msiRefresherNamespace = '{{ .msiCredentialsRefresher.k8s.namespace }}'
param msiRefresherServiceAccountName = '{{ .msiCredentialsRefresher.k8s.serviceAccountName }}'

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'

// ACR Resource IDs
param ocpAcrResourceId = '__ocpAcrResourceId__'
param svcAcrResourceId = '__svcAcrResourceId__'

// OIDC
param oidcStorageAccountName = '{{ .oidc.storageAccount.name }}'
param oidcStoragePrivateLinkLocation = '{{ .oidc.storageAccount.privateLinkLocation }}'
param oidcZoneRedundantMode = '{{ .oidc.storageAccount.zoneRedundantMode }}'
param oidcStorageAccountPublic = {{ .oidc.storageAccount.public }}
param azureFrontDoorResourceId = '__azureFrontDoorResourceId__'
param azureFrontDoorParentDnsZoneName = '{{ .oidc.frontdoor.subdomain }}.{{ .dns.svcParentZoneName }}'
param azureFrontDoorRegionalSubdomain = '{{ .dns.regionalSubdomain }}'
param azureFrontDoorKeyVaultName = '{{ .oidc.frontdoor.keyVault.name }}'
param azureFrontDoorKeyTagKey = '{{ .oidc.frontdoor.keyVault.name }}'
param azureFrontDoorKeyTagValue = '{{ .oidc.frontdoor.keyVault.name }}'
param azureFrontDoorUseManagedCertificates = {{ .oidc.frontdoor.useManagedCertificates }}

param globalMSIId = '__globalMSIId__'

param svcDNSZoneName = '{{ .dns.svcParentZoneName }}'
param regionalCXDNSZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.cxParentZoneName }}'
param regionalSvcDNSZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.svcParentZoneName }}'

param regionalResourceGroup = '{{ .regionRG }}'

param frontendIngressCertName = '{{ .frontend.cert.name }}'
param frontendIngressCertIssuer = '{{ .frontend.cert.issuer }}'
param genevaActionsServiceTag = '{{ .geneva.actions.serviceTag }}'

param fpaCertificateName = '{{ .firstPartyAppCertificate.name }}'
param fpaCertificateIssuer = '{{ .firstPartyAppCertificate.issuer }}'
param manageFpaCertificate = {{ .firstPartyAppCertificate.manage }}

// Azure Monitor Workspace
param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'

// MDSD / Genevabits
@description('The namespace of the logs')
param logsNamespace = '{{ .logs.mdsd.namespace }}'
param logsMSI = '{{ .logs.mdsd.msiName }}'
param logsServiceAccount = '{{ .logs.mdsd.serviceAccountName }}'

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId = '__logAnalyticsWorkspaceId__'

param svcNSPName = '{{ .svc.nsp.name }}'
param svcNSPAccessMode = '{{ .svc.nsp.accessMode }}'
param serviceKeyVaultAsignNSP = {{ .serviceKeyVault.assignNSP }}

// Geneva logging settings
param genevaCertificateDomain = '{{ .geneva.logs.certificateDomain }}'
param genevaCertificateIssuer = '{{ .geneva.logs.certificateIssuer }}'
param genevaRpLogsName = '{{ .geneva.logs.rp.secretName }}'
param genevaManageCertificates = {{ .geneva.logs.manageCertificates }}
