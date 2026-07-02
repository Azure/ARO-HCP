using '../templates/swift-vnet-permissions.bicep'

param deploymentMsiId = '__deploymentMsiId__'
param enableSwift = {{ .mgmt.aks.enableSwiftV2Vnet }}
