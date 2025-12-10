using '../templates/sre-tooling-infra.bicep'

param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVault.region }}'
param serviceKeyVaultSoftDelete = {{ .serviceKeyVault.softDelete }}
param serviceKeyVaultPrivate = {{ .serviceKeyVault.private }}
param serviceKeyVaultTagName = '{{ .serviceKeyVault.tagKey }}'
param serviceKeyVaultTagValue = '{{ .serviceKeyVault.tagValue }}'

// MI for resource access during pipeline runs
param globalMSIId = '__globalMSIId__'

// SP for KV certificate issuer registration
param kvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
