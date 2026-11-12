using '../templates/tenant-quota-identity.bicep'

param ownerIds = '{{ .entraAppOwnerIds }}'

param applicationName = '{{ .opstool.tenantQuota.tenants.redHat0.applicationName }}'
