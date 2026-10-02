using '../templates/kube-applier-cosmos.bicep'

import * as mi from '../modules/managed-identities.bicep'

param infrastructureIdentityResourceGroup = {{ .infrastructureIdentities.useLeased }}
  ? mi.getManagementIdentityResourceGroup('{{ .infrastructureIdentities.managementResourceGroups }}', '{{ .mgmt.stampIdentifier }}')
  : '{{ .mgmt.rg }}'

param rpCosmosDbAccountId = '__rpCosmosDbAccountId__'

// Kube Applier
param kubeApplierMIName = '{{ .kubeApplier.managedIdentityName }}'
param kubeApplierContainerName = '{{ .kubeApplier.cosmosContainerName }}'
param kubeApplierContainerMaxScale = {{ .kubeApplier.cosmosContainerMaxScale }}

// Clusters Service identity, used to grant read/write on the per-management-cluster
// kube-applier CosmosDB container
param csManagedIdentityPrincipalId = '__csManagedIdentityPrincipalId__'
