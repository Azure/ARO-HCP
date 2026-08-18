using '../templates/aks-nodepools.bicep'

// Wired from the cluster step outputs
param aksClusterName = '__aksClusterName__'
param nodeSubnetId = '__nodeSubnetId__'
param podSubnetId = '__podSubnetId__'

// User (worker) node pools
param userAgentPoolName = '{{ .svc.aks.userAgentPool.name }}'
param userAgentMinCount = {{ .svc.aks.userAgentPool.minCount }}
param userAgentMaxCount = {{ .svc.aks.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .svc.aks.userAgentPool.vmSize }}'
param userAgentPoolCount = {{ .svc.aks.userAgentPool.poolCount }}
param userOsDiskSizeGB = {{ .svc.aks.userAgentPool.osDiskSizeGB }}
param userAgentPoolZones = '{{ .svc.aks.userAgentPool.zones }}'
param userZoneRedundantMode = '{{ .svc.aks.userAgentPool.zoneRedundantMode }}'

// Infra node pools
param infraAgentPoolName = '{{ .svc.aks.infraAgentPool.name }}'
param infraAgentMinCount = {{ .svc.aks.infraAgentPool.minCount }}
param infraAgentMaxCount = {{ .svc.aks.infraAgentPool.maxCount }}
param infraAgentVMSize = '{{ .svc.aks.infraAgentPool.vmSize }}'
param infraAgentPoolCount = {{ .svc.aks.infraAgentPool.poolCount }}
param infraAgentPoolZones = '{{ .svc.aks.infraAgentPool.zones }}'
param infraOsDiskSizeGB = {{ .svc.aks.infraAgentPool.osDiskSizeGB }}
param infraZoneRedundantMode = '{{ .svc.aks.infraAgentPool.zoneRedundantMode }}'

param enableSwiftV2Nodepools = false
param aksUpgradeSettingsMaxSurge = '{{ .svc.aks.upgradeSettings.maxSurge }}'
param aksUpgradeSettingsMaxUnavailable = '{{ .svc.aks.upgradeSettings.maxUnavailable }}'
