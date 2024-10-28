using '../templates/image-sync.bicep'

param acrResourceGroup = 'global'

param keyVaultName = 'aro-hcp-dev-global-kv'
param bearerSecretName = 'bearer-secret'
param pullSecretName = 'component-sync-pull-secret'

param componentSyncImage = 'arohcpdev.azurecr.io/image-sync/component-sync:latest'
param svcAcrName = 'arohcpdev'
param repositoriesToSync = 'registry.k8s.io/external-dns/external-dns,quay.io/acm-d/rhtap-hypershift-operator,quay.io/app-sre/uhc-clusters-service'
param numberOfTags = 10
