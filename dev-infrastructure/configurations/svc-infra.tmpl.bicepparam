using '../templates/svc-infra.bicep'

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVault.region }}'
param serviceKeyVaultSoftDelete = {{ .serviceKeyVault.softDelete }}
param serviceKeyVaultPrivate = {{ .serviceKeyVault.private }}
param serviceKeyVaultTagName = '{{ .serviceKeyVault.tagKey }}'
param serviceKeyVaultTagValue = '{{ .serviceKeyVault.tagValue }}'

// MI for resource access during pipeline runs
param globalMSIId = '__globalMSIId__'

// Subscription where the service keyvault lives (supports cross-subscription scenarios)
param serviceKeyVaultSubscription = '__serviceKeyVaultSubscription__'

// SP for KV certificate issuer registration
param kvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'

// AKS prerequisites; cluster and pool writes are owned by aks-cluster-create.
param aksClusterName = '{{ .svc.aks.name }}'
param vnetAddressPrefix = '{{ .svc.aks.vnetAddressPrefix }}'
param subnetPrefix = '{{ .svc.aks.subnetPrefix }}'
param podSubnetPrefix = '{{ .svc.aks.podSubnetPrefix }}'
param aksKeyVaultName = '{{ .svc.aks.etcd.name }}'
param aksKeyVaultTagName = '{{ .svc.aks.etcd.tagKey }}'
param aksKeyVaultTagValue = '{{ .svc.aks.etcd.tagValue }}'
param aksEtcdKVEnableSoftDelete = {{ .svc.aks.etcd.softDelete }}
param aksClusterOutboundIPAddressIPTags = '{{ .svc.aks.clusterOutboundIPAddressIPTags }}'
param regionalResourceGroup = '{{ .regionRG }}'
param istioIngressGatewayIPAddressName = '{{ .svc.istio.ingressGatewayIPAddressName }}'
param istioIngressGatewayIPAddressIPTags = '{{ .svc.istio.ingressGatewayIPAddressIPTags }}'
param opsIngressGatewayIPAddressName = '{{ .svc.opsIngress.gateway.ipAddressName }}'
param opsIngressGatewayIPAddressTags = '{{ .svc.opsIngress.gateway.ipAddressTags }}'
param genevaActionsServiceTag = '{{ .geneva.actions.serviceTag }}'
param sreServiceTag = '{{ .administration.sreServiceTag }}'
param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'
param leasedAksIdentityName = '{{ .infrastructureIdentities.serviceAksIdentityName }}'
