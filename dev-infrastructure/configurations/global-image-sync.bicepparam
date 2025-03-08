using '../templates/global-image-sync.bicep'

param containerAppEnvName = 'aro-hcp-image-sync'

param acrResourceGroup = 'global'
param keyVaultName = 'arohcp-imagesync-dev'
param keyVaultPrivate = false
param keyVaultSoftDelete = false

param componentSyncPullSecretName = 'component-sync-pull-secret'
param componentSyncImage = 'arohcpsvcdev.azurecr.io/image-sync/component-sync@sha256:d838c4910bc53a5583dd501ed7e3ab08aa7c08b45b5997c90764c65ceef01a8f'
param componentSyncEnabed = true
param componentSyncSecrets = 'quay.io:bearer-secret'

param svcAcrName = 'arohcpsvcdev'

param ocpAcrName = 'arohcpocpdev'
param ocpPullSecretName = 'pull-secret'
param repositoriesToSync = 'quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro,quay.io/acm-d/rhtap-hypershift-operator,quay.io/app-sre/uhc-clusters-service,quay.io/package-operator/package-operator-package,quay.io/package-operator/package-operator-manager'
param ocMirrorImage = 'arohcpsvcdev.azurecr.io/image-sync/oc-mirror@sha256:4affed9ff6397a5c44e9d1451fd58667f56e826b122819ccb6e1e8e045803c18'
param ocMirrorEnabled = true

param numberOfTags = 10
