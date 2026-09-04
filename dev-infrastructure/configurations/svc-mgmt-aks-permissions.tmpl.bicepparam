using '../templates/svc-mgmt-aks-permissions.bicep'

// AKS cluster name
param aksClusterName = '{{ .mgmt.aks.name }}'

// VNet name
param vnetName = 'aks-net'

// Session Gate identity
// used for AKS access
param sessiongateMIResourceId = '__sessiongateMIResourceId__'

// Fleet identity
// used for AKS read access (node pool scaling)
param fleetMIResourceId = '__fleetMIResourceId__'
