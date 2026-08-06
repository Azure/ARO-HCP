using '../templates/aks-nodepools.bicep'

// Wired from the cluster step outputs
param aksClusterName = '__aksClusterName__'
param nodeSubnetId = '__nodeSubnetId__'
param podSubnetId = '__podSubnetId__'

// User (worker) node pools; pool names match the vars in opstool-cluster.bicep
param userAgentPoolName = 'user'
param userAgentMinCount = {{ .opstool.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .opstool.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .opstool.aks.userAgentPool.vmSize }}'
param userAgentPoolCount = {{ .opstool.aks.userAgentPool.count }}
param userOsDiskSizeGB = {{ .opstool.aks.userAgentPool.osDiskSizeGB }}
param userAgentPoolZones = '{{ .opstool.aks.userAgentPool.zones }}'
param userZoneRedundantMode = '{{ .opstool.aks.userAgentPool.zoneRedundantMode }}'

// Infra node pools
param infraAgentPoolName = 'infra'
param infraAgentMinCount = {{ .opstool.aks.infraAgentPool.minCount }}
param infraAgentMaxCount = {{ .opstool.aks.infraAgentPool.maxCount }}
param infraAgentVMSize = '{{ .opstool.aks.infraAgentPool.vmSize }}'
param infraAgentPoolCount = {{ .opstool.aks.infraAgentPool.count }}
param infraAgentPoolZones = '{{ .opstool.aks.infraAgentPool.zones }}'
param infraOsDiskSizeGB = {{ .opstool.aks.infraAgentPool.osDiskSizeGB }}
param infraZoneRedundantMode = '{{ .opstool.aks.infraAgentPool.zoneRedundantMode }}'

param enableSwiftV2Nodepools = false
param aksUpgradeSettingsMaxSurge = '{{ .opstool.aks.upgradeSettings.maxSurge }}'
param aksUpgradeSettingsMaxUnavailable = '{{ .opstool.aks.upgradeSettings.maxUnavailable }}'
