using '../modules/cluster-service/cluster-service-lookup.bicep'

param imagePullerMsiName = 'image-puller'
param csMsiName = '{{ .clustersService.managedIdentityName }}'
param regionalResourceGroup = '{{ .regionRG }}'
param regionalOidcStorageAccountName = '{{ .oidc.storageAccount.name }}'
param afdOidcBaseEndpoint = 'https://{{ .dns.regionalSubdomain }}.{{ .oidc.frontdoor.subdomain }}.{{ .dns.svcParentZoneName }}/'
param useAzureDB = {{ .clustersService.postgres.deploy }}
param postgresName = '{{ .clustersService.postgres.name }}'
