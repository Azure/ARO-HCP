using '../templates/mgmt-cluster.bicep'

param kubernetesVersion = '{{ .kubernetesVersion}}'
param vnetAddressPrefix = '{{ .vnetAddressPrefix }}'
param subnetPrefix = '{{ .subnetPrefix }}'
param podSubnetPrefix = '{{ .podSubnetPrefix }}'
param aksClusterName = '{{ .aksName }}'
param aksKeyVaultName = '{{ .mgmtEtcdKVName }}'
param aksEtcdKVEnableSoftDelete = {{ .mgmtEtcdKVSoftDelete }}
param systemAgentMinCount = {{ .mgmtSystemAgentPoolMinCount}}
param systemAgentMaxCount = {{ .mgmtSystemAgentPoolMaxCount }}
param systemAgentVMSize = '{{ .mgmtSystemAgentPoolVmSize }}'
param aksSystemOsDiskSizeGB = {{ .mgmtSystemAgentPoolOsDiskSizeGB }}
param userAgentMinCount = {{ .mgmtUserAgentPoolMinCount }}
param userAgentMaxCount = {{ .mgmtUserAgentPoolMaxCount }}
param userAgentVMSize = '{{ .mgmtUserAgentPoolVmSize }}'
param aksUserOsDiskSizeGB = {{ .mgmtUserAgentPoolOsDiskSizeGB }}
param userAgentPoolAZCount = {{ .mgmtUserAgentPoolAzCount }}

param maestroConsumerName = '{{ .maestroConsumerName }}'
param maestroKeyVaultName = '{{ .maestroKeyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestroEventgridName }}'
param maestroCertDomain = '{{ .maestroCertDomain }}'

param regionalDNSZoneName = '{{ .regionalDNSSubdomain}}.{{ .baseDnsZoneName }}'

param acrPullResourceGroups = ['{{ .serviceComponentAcrResourceGroups }}']

param regionalResourceGroup = '{{ .regionRG }}'
