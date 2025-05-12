using '../templates/mgmt-cluster.bicep'

// AKS
param kubernetesVersion = '{{ .mgmt.aks.kubernetesVersion }}'
param vnetAddressPrefix = '{{ .mgmt.aks.vnetAddressPrefix }}'
param subnetPrefix = '{{ .mgmt.aks.subnetPrefix }}'
param podSubnetPrefix = '{{ .mgmt.aks.podSubnetPrefix }}'
param aksClusterName = '{{ .mgmt.aks.name }}'
param aksKeyVaultName = '{{ .mgmt.aks.etcd.kvName }}'
param aksEtcdKVEnableSoftDelete = {{ .mgmt.aks.etcd.kvSoftDelete }}
param systemAgentMinCount = {{ .mgmt.aks.systemAgentPool.minCount}}
param systemAgentMaxCount = {{ .mgmt.aks.systemAgentPool.maxCount }}
param systemAgentVMSize = '{{ .mgmt.aks.systemAgentPool.vmSize }}'
param aksSystemOsDiskSizeGB = {{ .mgmt.aks.systemAgentPool.osDiskSizeGB }}
param userAgentMinCount = {{ .mgmt.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .mgmt.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .mgmt.aks.userAgentPool.vmSize }}'
param userAgentPoolAZCount = {{ .mgmt.aks.userAgentPool.azCount }}
param infraAgentMinCount = {{ .mgmt.aks.infraAgentPool.minCount }}
param infraAgentMaxCount = {{ .mgmt.aks.infraAgentPool.maxCount }}
param infraAgentVMSize = '{{ .mgmt.aks.infraAgentPool.vmSize }}'
param infraAgentPoolAZCount = {{ .mgmt.aks.infraAgentPool.azCount }}
param aksInfraOsDiskSizeGB = {{ .mgmt.aks.infraAgentPool.osDiskSizeGB }}
param aksUserOsDiskSizeGB = {{ .mgmt.aks.userAgentPool.osDiskSizeGB }}
param aksClusterOutboundIPAddressIPTags = '{{ .mgmt.aks.clusterOutboundIPAddressIPTags }}'
param aksNetworkDataplane = '{{ .mgmt.aks.networkDataplane }}'
param aksNetworkPolicy = '{{ .mgmt.aks.networkDataplane }}'
param aksEnableSwift = {{ .mgmt.aks.enableSwiftV2 }}

// Maestro
param maestroConsumerName = '{{ .maestro.agent.consumerName }}'
param maestroEventGridNamespaceId = '__maestroEventGridNamespaceId__'
param maestroCertDomain = '{{ .maestro.certDomain }}'
param maestroCertIssuer = '{{ .maestro.certIssuer }}'
param regionalSvcDNSZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.svcParentZoneName }}'


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
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

// Azure Monitor Workspace
param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'

// MDSD / Genevabits
@description('The namespace of the logs')
param logsNamespace = '{{ .logs.mdsd.namespace }}'
param logsMSI = '{{ .logs.mdsd.msiName }}'
param logsServiceAccount = '{{ .logs.mdsd.serviceAccountName }}'

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId = '__logAnalyticsWorkspaceId__'

