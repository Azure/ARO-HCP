using '../modules/adminapi/adminapi-lookup.bicep'

param adminApiMsiName = '{{ .adminApi.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
