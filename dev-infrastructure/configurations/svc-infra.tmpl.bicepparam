using '../templates/svc-infra.bicep'

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVault.region }}'
param serviceKeyVaultSoftDelete = {{ .serviceKeyVault.softDelete }}
param serviceKeyVaultPrivate = {{ .serviceKeyVault.private }}

// MI for resource access during pipeline runs
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'

// SP for KV certificate issuer registration
param svcKvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
