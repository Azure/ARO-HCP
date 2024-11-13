using '../templates/mgmt-cluster.bicep'

// AKS
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

// Maestro
param maestroConsumerName = '{{ .maestroConsumerName }}'
param maestroKeyVaultName = '{{ .maestroKeyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestroEventgridName }}'
param maestroCertDomain = '{{ .maestroCertDomain }}'

// Hypershift
param hypershiftNamespace = '{{ .hypershiftNamespace }}'
param externalDNSManagedIdentityName = '{{ .externalDNSManagedIdentityName }}'
param externalDNSServiceAccountName = '{{ .externalDNSServiceAccountName }}'

// DNS
param regionalDNSZoneName = '{{ .regionalDNSSubdomain}}.{{ .baseDnsZoneName }}'

// ACR
param acrPullResourceGroups = ['{{ .serviceComponentAcrResourceGroups }}']

// Region
param regionalResourceGroup = '{{ .regionRG }}'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVaultName }}'
param cxKeyVaultPrivate = {{ .cxKeyVaultPrivate }}
param cxKeyVaultSoftDelete = {{ .cxKeyVaultSoftDelete }}

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVaultName }}'
param msiKeyVaultPrivate = {{ .msiKeyVaultPrivate }}
param msiKeyVaultSoftDelete = {{ .msiKeyVaultSoftDelete }}

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVaultName }}'
param mgmtKeyVaultPrivate = {{ .mgmtKeyVaultPrivate }}
param mgmtKeyVaultSoftDelete = {{ .mgmtKeyVaultSoftDelete }}
