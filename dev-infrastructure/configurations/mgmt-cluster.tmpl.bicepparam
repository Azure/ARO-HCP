using '../templates/mgmt-cluster.bicep'

// AKS
param kubernetesVersion = '{{ .mgmt.aks.kubernetesVersion }}'
param vnetAddressPrefix = '{{ .mgmt.aks.vnetAddressPrefix }}'
param subnetPrefix = '{{ .mgmt.aks.subnetPrefix }}'
param podSubnetPrefix = '{{ .mgmt.aks.podSubnetPrefix }}'
param aksClusterName = '{{ .mgmt.aks.name }}'
param aksKeyVaultName = '{{ .mgmt.aks.etcd.name }}'
param aksKeyVaultTagName = '{{ .mgmt.aks.etcd.tagKey }}'
param aksKeyVaultTagValue = '{{ .mgmt.aks.etcd.tagValue }}'
param aksEtcdKVEnableSoftDelete = {{ .mgmt.aks.etcd.softDelete }}
param systemAgentPoolName = '{{ .mgmt.aks.systemAgentPool.name }}'
param systemAgentMinCount = {{ .mgmt.aks.systemAgentPool.minCount}}
param systemAgentMaxCount = {{ .mgmt.aks.systemAgentPool.maxCount }}
param systemAgentVMSize = '{{ .mgmt.aks.systemAgentPool.vmSize }}'
param systemAgentPoolZones = '{{ .mgmt.aks.systemAgentPool.zones }}'
param systemOsDiskSizeGB = {{ .mgmt.aks.systemAgentPool.osDiskSizeGB }}
param systemZoneRedundantMode = '{{ .mgmt.aks.systemAgentPool.zoneRedundantMode }}'
param aksClusterOutboundIPAddressIPTags = '{{ .mgmt.aks.clusterOutboundIPAddressIPTags }}'
param aksNetworkDataplane = '{{ .mgmt.aks.networkDataplane }}'
param aksNetworkPolicy = '{{ .mgmt.aks.networkPolicy }}'
param aksUpgradeSettingsMaxSurge = '{{ .mgmt.aks.upgradeSettings.maxSurge }}'
param aksUpgradeSettingsMaxUnavailable = '{{ .mgmt.aks.upgradeSettings.maxUnavailable }}'

// Maestro
param maestroConsumerMIName = '{{ .maestro.agent.managedIdentityName }}'
param maestroConsumerNamespace = '{{ .maestro.agent.k8s.namespace }}'
param maestroConsumerServiceAccountName = '{{ .maestro.agent.k8s.serviceAccountName }}'

// Mgmt Agent
param mgmtAgentMIName = '{{ .mgmtAgent.managedIdentityName }}'
param mgmtAgentNamespace = '{{ .mgmtAgent.k8s.namespace }}'
param mgmtAgentServiceAccountName = '{{ .mgmtAgent.k8s.serviceAccountName }}'

param maestroConsumerName = '{{ .maestro.agent.consumerName }}'
param maestroConsumerCertSAN = '{{ .maestro.agent.certSAN }}'
param maestroCertIssuer = '{{ .maestro.certIssuer }}'
param maestroEventGridNamespaceId = '__maestroEventGridNamespaceId__'

// Kube Applier
param kubeApplierMIName = '{{ .kubeApplier.managedIdentityName }}'
param kubeApplierNamespace = '{{ .kubeApplier.k8s.namespace }}'
param kubeApplierServiceAccountName = '{{ .kubeApplier.k8s.serviceAccountName }}'
param kubeApplierContainerName = '{{ .kubeApplier.cosmosContainerName }}'
param kubeApplierContainerMaxScale = {{ .kubeApplier.cosmosContainerMaxScale }}
param rpCosmosDbAccountId = '__rpCosmosDbAccountId__'
param rpCosmosDbPrivate = {{ .frontend.cosmosDB.private }}

// ACR
param ocpAcrResourceId = '__ocpAcrResourceId__'
param svcAcrResourceId = '__svcAcrResourceId__'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'

// MI for deployment scripts
param globalMSIId = '__globalMSIId__'

// Azure Monitor Workspace
param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'

// MDSD / Genevabits
param logsNamespace = '{{ .logs.mdsd.namespace }}'
param logsMSI = '{{ .logs.mdsd.msiName }}'
param logsServiceAccount = '{{ .logs.mdsd.serviceAccountName }}'

// Geneva logging settings
param genevaRpLogsName = '{{ .geneva.logs.rp.secretName }}'
param genevaClusterLogsName = '{{ .geneva.logs.cluster.secretName }}'

// Alert rules tag value
param owningTeamTagValue = '{{ .monitoring.alertRuleOwningTeamTag }}'
param aksClusterTags = '{{ .mgmt.aks.tags }}'

// HCP Backups Storage Account
param hcpBackupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccount.name }}'

// Audit Logs Event Hub
param auditLogsEventHubName = '{{ .auditLogsEventHub.name }}'
param auditLogsEventHubAuthRuleId = '__auditLogsEventHubAuthRuleId__'
