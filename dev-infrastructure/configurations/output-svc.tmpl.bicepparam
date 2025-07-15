using '../templates/output-svc.bicep'

param csMIName = '{{ .clustersService.managedIdentityName }}'
param msiRefresherMIName = '{{ .msiCredentialsRefresher.managedIdentityName }}'
