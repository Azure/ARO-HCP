using '../templates/swift-vnet-permissions.bicep'

param deploymentMsiId = '__deploymentMsiId__'
param swiftVnetIdentityResourceId = '{{ .mgmt.swiftVnetIdentityResourceId }}'
