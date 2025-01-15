using '../templates/svc-infra.bicep'

param certName = '{{ .frontend.cert.name }}'
param serviceKeyVaultName = '{{ .serviceKeyVault.name }}'
param serviceKeyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'
param serviceKeyVaultLocation = '{{ .serviceKeyVault.region }}'
param serviceKeyVaultSoftDelete = {{ .serviceKeyVault.softDelete }}
param serviceKeyVaultPrivate = {{ .serviceKeyVault.private }}

param regionalSvcDNSZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.svcParentZoneName }}'


// MI for deployment scripts
param aroDevopsMsiId = '{{ .aroDevopsMsiId }}'
// SP for KV certificate issuer registration
param svcKvCertOfficerPrincipalId = '{{ .kvCertOfficerPrincipalId }}'
