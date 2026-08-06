using '../templates/aks-nodepools.bicep'

// Wired from the cluster step outputs
param aksClusterName = '__aksClusterName__'
param nodeSubnetId = '__nodeSubnetId__'
param podSubnetId = '__podSubnetId__'

// User (worker) node pools
param userAgentPoolName = '{{ .mgmt.aks.userAgentPool.name }}'
param userAgentMinCount = {{ .mgmt.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .mgmt.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .mgmt.aks.userAgentPool.vmSize }}'
param userAgentPoolCount = {{ .mgmt.aks.userAgentPool.poolCount }}
param userOsDiskSizeGB = {{ .mgmt.aks.userAgentPool.osDiskSizeGB }}
param userAgentPoolZones = '{{ .mgmt.aks.userAgentPool.zones }}'
param userZoneRedundantMode = '{{ .mgmt.aks.userAgentPool.zoneRedundantMode }}'
param userSecondaryNicCount = {{ .mgmt.aks.userAgentPool.secondaryNicCount }}

// Infra node pools
param infraAgentPoolName = '{{ .mgmt.aks.infraAgentPool.name }}'
param infraAgentMinCount = {{ .mgmt.aks.infraAgentPool.minCount }}
param infraAgentMaxCount = {{ .mgmt.aks.infraAgentPool.maxCount }}
param infraAgentVMSize = '{{ .mgmt.aks.infraAgentPool.vmSize }}'
param infraAgentPoolCount = {{ .mgmt.aks.infraAgentPool.poolCount }}
param infraAgentPoolZones = '{{ .mgmt.aks.infraAgentPool.zones }}'
param infraOsDiskSizeGB = {{ .mgmt.aks.infraAgentPool.osDiskSizeGB }}
param infraZoneRedundantMode = '{{ .mgmt.aks.infraAgentPool.zoneRedundantMode }}'

param enableSwiftV2Nodepools = true
param aksUpgradeSettingsMaxSurge = '{{ .mgmt.aks.upgradeSettings.maxSurge }}'
param aksUpgradeSettingsMaxUnavailable = '{{ .mgmt.aks.upgradeSettings.maxUnavailable }}'
