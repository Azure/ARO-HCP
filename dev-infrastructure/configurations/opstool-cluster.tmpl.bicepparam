using '../templates/opstool-cluster.bicep'

param kubernetesVersion = '{{ .opstool.aks.kubernetesVersion }}'
param vnetAddressPrefix = '{{ .opstool.aks.vnetAddressPrefix }}'
param subnetPrefix = '{{ .opstool.aks.subnetPrefix }}'
param podSubnetPrefix = '{{ .opstool.aks.podSubnetPrefix }}'
param aksClusterName = '{{ .opstool.aks.name }}'
// Passed here to seed revisions at initial cluster create. Opstool clusters
// don't run the IstioUpgrade pipeline step, so bicep must set the value.
// SVC clusters omit this — see aks-cluster-base.bicep for the decoupling rationale.
param istioVersions = '{{ .opstool.istio.versions }}'
param aksKeyVaultName = '{{ .opstool.aks.etcd.name }}'
param aksKeyVaultTagName = '{{ .opstool.aks.etcd.tagKey }}'
param aksKeyVaultTagValue = '{{ .opstool.aks.etcd.tagValue }}'
param aksEtcdKVEnableSoftDelete = {{ .opstool.aks.etcd.softDelete }}
param systemAgentMinCount = {{ .opstool.aks.systemAgentPool.minCount }}
param systemAgentMaxCount = {{ .opstool.aks.systemAgentPool.maxCount }}
param systemAgentPoolZones = '{{ .opstool.aks.systemAgentPool.zones }}'
param systemAgentVMSize = '{{ .opstool.aks.systemAgentPool.vmSize }}'
param systemZoneRedundantMode = '{{ .opstool.aks.systemAgentPool.zoneRedundantMode }}'
param aksSystemOsDiskSizeGB = {{ .opstool.aks.systemAgentPool.osDiskSizeGB }}
param aksNetworkDataplane = '{{ .opstool.aks.networkDataplane }}'
param aksNetworkPolicy = '{{ .opstool.aks.networkPolicy }}'
param aksClusterOutboundIPAddressIPTags = '{{ .opstool.aks.clusterOutboundIPAddressIPTags }}'
param owningTeamTagValue = '{{ .monitoring.alertRuleOwningTeamTag }}'
param aksClusterTags = '{{ .opstool.aks.tags }}'
param azureMonitorWorkspaceName = '{{ .opstool.monitoring.workspaceName }}'
param workloadKVName = '{{ .opstool.keyVault.name }}'
param svcAcrName = '{{ .svc.acr.name }}'
param svcAcrResourceGroupName = '{{ .svc.acr.resourceGroup }}'
