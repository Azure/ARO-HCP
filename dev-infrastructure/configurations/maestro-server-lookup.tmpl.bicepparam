using '../modules/maestro/maestro-server-lookup.bicep'

param maestroMsiName = '{{ .maestro.server.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'