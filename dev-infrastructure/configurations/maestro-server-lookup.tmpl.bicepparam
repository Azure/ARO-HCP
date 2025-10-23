using '../modules/maestro/maestro-server-lookup.bicep'

param maestroMsiName = '{{ .maestro.server.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param useAzureDB = {{ .maestro.postgres.deploy }}
param postgresName = '{{ .maestro.postgres.name }}'
param regionalResourceGroup = '{{ .regionRG }}'