using '../templates/kube-applier-cosmos.bicep'

param rpCosmosDbAccountId = '__rpCosmosDbAccountId__'

// Kube Applier
param kubeApplierMIName = '{{ .kubeApplier.managedIdentityName }}'
param kubeApplierContainerName = '{{ .kubeApplier.cosmosContainerName }}'
param kubeApplierContainerMaxScale = {{ .kubeApplier.cosmosContainerMaxScale }}

// Clusters Service identity, used to grant read/write on the per-management-cluster
// kube-applier CosmosDB container
param csManagedIdentityPrincipalId = '__csManagedIdentityPrincipalId__'
