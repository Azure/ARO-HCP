using '../templates/kube-applier-lookup.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param managementIdentityResourceGroups = '{{ .infrastructureIdentities.managementResourceGroups }}'
param stampIdentifier = '{{ .mgmt.stampIdentifier }}'

param imagePullerMsiName = 'image-puller'
param kubeApplierMsiName = '{{ .kubeApplier.managedIdentityName }}'
