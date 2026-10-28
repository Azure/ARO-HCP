using '../templates/output-svc-istio-bootstrap.bicep'

param aksClusterName = '{{ .svc.aks.name }}'
param configuredVersions = '{{ .svc.istio.versions }}'
