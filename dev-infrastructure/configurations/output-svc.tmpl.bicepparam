using '../templates/output-svc.bicep'

param csMIName = '{{ .clustersService.managedIdentityName }}'
param msiRefresherMIName = '{{ .msiCredentialsRefresher.managedIdentityName }}'
param adminApiMIName = '{{ .adminApi.managedIdentityName }}'
