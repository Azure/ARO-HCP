using '../templates/mgmt-cluster.bicep'

// AKS
param kubernetesVersion = '{{ .kubernetesVersion}}'
param vnetAddressPrefix = '{{ .vnetAddressPrefix }}'
param subnetPrefix = '{{ .subnetPrefix }}'
param podSubnetPrefix = '{{ .podSubnetPrefix }}'
param aksClusterName = '{{ .aksName }}'
param aksKeyVaultName = '{{ .mgmt.etcd.kvName }}'
param aksEtcdKVEnableSoftDelete = {{ .mgmt.etcd.kvSoftDelete }}
param systemAgentMinCount = {{ .mgmt.systemAgentPool.minCount}}
param systemAgentMaxCount = {{ .mgmt.systemAgentPool.maxCount }}
param systemAgentVMSize = '{{ .mgmt.systemAgentPool.vmSize }}'
param aksSystemOsDiskSizeGB = {{ .mgmt.systemAgentPool.osDiskSizeGB }}
param userAgentMinCount = {{ .mgmt.userAgentPool.minCount }}
param userAgentMaxCount = {{ .mgmt.userAgentPool.maxCount }}
param userAgentVMSize = '{{ .mgmt.userAgentPool.vmSize }}'
param aksUserOsDiskSizeGB = {{ .mgmt.userAgentPool.osDiskSizeGB }}
param userAgentPoolAZCount = {{ .mgmt.userAgentPool.azCount }}

// Maestro
param maestroConsumerName = '{{ .maestro.consumerName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroCertDomain = '{{ .maestro.certDomain }}'

// Hypershift
param hypershiftNamespace = '{{ .hypershift.namespace }}'
param externalDNSManagedIdentityName = '{{ .hypershift.externalDNSManagedIdentityName }}'
param externalDNSServiceAccountName = '{{ .hypershift.externalDNSServiceAccountName }}'

// DNS
param regionalDNSZoneName = '{{ .regionalDNSSubdomain}}.{{ .baseDnsZoneName }}'

// ACR
param acrPullResourceGroups = ['{{ .serviceComponentAcrResourceGroups }}']

// Region
param regionalResourceGroup = '{{ .regionRG }}'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'
param cxKeyVaultPrivate = {{ .cxKeyVault.private }}
param cxKeyVaultSoftDelete = {{ .cxKeyVault.softDelete }}

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'
param msiKeyVaultPrivate = {{ .msiKeyVault.private }}
param msiKeyVaultSoftDelete = {{ .msiKeyVault.softDelete }}

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'
param mgmtKeyVaultPrivate = {{ .mgmtKeyVault.private }}
param mgmtKeyVaultSoftDelete = {{ .mgmtKeyVault.softDelete }}

// Cluster Service identity
// used for Key Vault access
param clusterServicePrincipalId = '{{ .mgmt.clusterServicePrincipalId }}'

// MI for deployment scripts
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'
