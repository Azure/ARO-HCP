using '../templates/swift-vnet-permissions.bicep'

param swiftVnetIdentityResourceId = '__swiftVnetIdentityResourceId__'
param configuredSwiftVnetIdentityResourceId = '{{ .mgmt.swiftVnetIdentityResourceId }}'
