using '../templates/cluster-service-lookup.bicep'

param imagePullerMsiName = 'image-puller'
param csMsiName = '{{ .clustersService.managedIdentityName }}'
