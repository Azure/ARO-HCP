using '../templates/svc-mgmt-aks-permissions.bicep'

// AKS cluster name
param aksClusterName = '{{ .mgmt.aks.name }}'

// Session Gate identity
// used for AKS access
param sessiongateMIResourceId = '__sessiongateMIResourceId__'

// Admin API identity
// used for AKS access (Holmes investigation)
param adminApiMIResourceId = '__adminApiMIResourceId__'
