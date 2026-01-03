using '../templates/sre-tooling-cluster.bicep'

// Location
param location = 'westus3'

// AKS Cluster
// Note: This will be overridden by Makefile based on SRE_TOOLING_ENV
// Default: 'sre-tooling-aks' for dev, 'pers-westus3-sre-tooling' for pers
param aksClusterName = 'sre-tooling-aks'
param kubernetesVersion = '1.32'
param vnetAddressPrefix = '10.0.0.0/16'
param subnetPrefix = '10.0.0.0/24'
param podSubnetPrefix = '10.0.1.0/24'

// System Agent Pool
param systemAgentMinCount = 2
param systemAgentMaxCount = 3
param systemAgentPoolCount = 1
param systemAgentPoolZones = '1,2,3'
param systemAgentVMSize = 'Standard_D2s_v3'
param systemZoneRedundantMode = 'Zone'
param aksSystemOsDiskSizeGB = 32

// User Agent Pool
param userAgentMinCount = 1
param userAgentMaxCount = 3
param userAgentVMSize = 'Standard_D2s_v3'
param userAgentPoolCount = 1
param userAgentPoolZones = '1,2,3'
param userZoneRedundantMode = 'Zone'
param userOsDiskSizeGB = 32

// Infra Agent Pool (for Prometheus)
param infraAgentMinCount = 1
param infraAgentMaxCount = 2
param infraAgentVMSize = 'Standard_D4s_v3'
param infraAgentPoolCount = 1
param infraAgentPoolZones = '1,2,3'
param infraZoneRedundantMode = 'Zone'
param infraOsDiskSizeGB = 64

// Network
param aksNetworkDataplane = 'azure'
param aksNetworkPolicy = 'azure'

// Key Vault for AKS etcd
param aksKeyVaultName = ''
param aksKeyVaultTagName = 'aro-hcp-environment'
param aksKeyVaultTagValue = 'dev'
param aksEtcdKVEnableSoftDelete = true
param aksClusterOutboundIPAddressIPTags = ''

// These will be overridden via command line
param svcAcrResourceId = ''
param serviceKeyVaultName = ''
param serviceKeyVaultResourceGroup = ''
param regionalResourceGroup = ''
param globalMSIId = ''
param azureMonitoringWorkspaceId = ''
param logsNamespace = 'logs'
param logsMSI = 'logs-msi'
param logsServiceAccount = 'logs-service-account'
param adminApiMIName = ''
param adminApiNamespace = 'admin-api'
param adminApiServiceAccountName = 'admin-api-service-account'

