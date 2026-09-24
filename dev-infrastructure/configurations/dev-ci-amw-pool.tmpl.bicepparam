using '../templates/dev-ci-amw-pool.bicep'

param location = '{{ .ci.dev.amwPool.location }}'
param servicesNamePrefix = '{{ .ci.dev.amwPool.services.namePrefix }}'
param servicesCount = {{ .ci.dev.amwPool.services.size }}
param hcpsNamePrefix = '{{ .ci.dev.amwPool.hcps.namePrefix }}'
param hcpsCount = {{ .ci.dev.amwPool.hcps.size }}
