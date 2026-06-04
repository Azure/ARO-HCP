using '../templates/svc-mgmt-aks-permissions.bicep'

// AKS cluster name
param aksClusterName = '{{ .mgmt.aks.name }}'

// Session Gate identity
// used for AKS access
param sessiongateMIResourceId = '__sessiongateMIResourceId__'

// FPA service principal object ID
// used for AKS access (Holmes investigation — admin API uses FPA to reach mgmt clusters)
param fpaObjectId = '{{ .firstPartyAppObjectId }}'
