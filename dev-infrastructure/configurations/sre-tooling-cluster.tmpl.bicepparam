using '../templates/sre-tooling-cluster.bicep'

// AKS
param kubernetesVersion = '{{ .sretooling.aks.kubernetesVersion }}'
param vnetAddressPrefix = '{{ .sretooling.aks.vnetAddressPrefix }}'
param subnetPrefix = '{{ .sretooling.aks.subnetPrefix }}'
param podSubnetPrefix = '{{ .sretooling.aks.podSubnetPrefix }}'
param aksClusterName = '{{ .sretooling.aks.name }}'
param aksKeyVaultName = '{{ .sretooling.aks.etcd.name }}'
param aksKeyVaultTagName = '{{ .sretooling.aks.etcd.tagKey }}'
param aksKeyVaultTagValue = '{{ .sretooling.aks.etcd.tagValue }}'
param aksEtcdKVEnableSoftDelete = {{ .sretooling.aks.etcd.softDelete }}
param systemAgentMinCount = {{ .sretooling.aks.systemAgentPool.minCount}}
param systemAgentMaxCount = {{ .sretooling.aks.systemAgentPool.maxCount }}
param systemAgentPoolCount = {{ .sretooling.aks.systemAgentPool.poolCount }}
param systemAgentPoolZones = '{{ .sretooling.aks.systemAgentPool.zones }}'
param systemAgentVMSize = '{{ .sretooling.aks.systemAgentPool.vmSize }}'
param systemZoneRedundantMode = '{{ .sretooling.aks.systemAgentPool.zoneRedundantMode }}'
param aksSystemOsDiskSizeGB = {{ .sretooling.aks.systemAgentPool.osDiskSizeGB }}
param userAgentMinCount = {{ .sretooling.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .sretooling.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .sretooling.aks.userAgentPool.vmSize }}'
param userAgentPoolCount = {{ .sretooling.aks.userAgentPool.poolCount }}
param userAgentPoolZones = '{{ .sretooling.aks.userAgentPool.zones }}'
param userZoneRedundantMode = '{{ .sretooling.aks.userAgentPool.zoneRedundantMode }}'
param infraAgentMinCount = {{ .sretooling.aks.infraAgentPool.minCount }}
param infraAgentMaxCount = {{ .sretooling.aks.infraAgentPool.maxCount }}
param infraAgentVMSize = '{{ .sretooling.aks.infraAgentPool.vmSize }}'
param infraAgentPoolCount = {{ .sretooling.aks.infraAgentPool.poolCount }}
param infraAgentPoolZones = '{{ .sretooling.aks.infraAgentPool.zones }}'
param infraZoneRedundantMode = '{{ .sretooling.aks.infraAgentPool.zoneRedundantMode }}'
param infraOsDiskSizeGB = {{ .sretooling.aks.infraAgentPool.osDiskSizeGB }}
param userOsDiskSizeGB = {{ .sretooling.aks.userAgentPool.osDiskSizeGB }}
param aksClusterOutboundIPAddressIPTags = '{{ .sretooling.aks.clusterOutboundIPAddressIPTags }}'
param aksNetworkDataplane = '{{ .sretooling.aks.networkDataplane }}'
param aksNetworkPolicy = '{{ .sretooling.aks.networkPolicy }}'

// ACR Resource ID
param svcAcrResourceId = '__svcAcrResourceId__'

// Service Key Vault
param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'

// Regional infrastructure
param regionalResourceGroup = '{{ .regionRG }}'

// Global MSI
param globalMSIId = '__globalMSIId__'

// Azure Monitor Workspace
param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'

// MDSD / Genevabits
param logsNamespace = '{{ .logs.mdsd.namespace }}'
param logsMSI = '{{ .logs.mdsd.msiName }}'
param logsServiceAccount = '{{ .logs.mdsd.serviceAccountName }}'

// Admin API
param adminApiMIName = '{{ .sretooling.adminApi.managedIdentityName }}'
param adminApiNamespace = '{{ .adminApi.k8s.namespace }}'
param adminApiServiceAccountName = '{{ .adminApi.k8s.serviceAccountName }}'
