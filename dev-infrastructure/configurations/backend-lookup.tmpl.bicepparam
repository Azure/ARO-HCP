using '../templates/backend-lookup.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'

param backendMsiName = '{{ .backend.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param regionalResourceGroup = '{{ .regionRG }}'
param regionalOidcStorageAccountName = '{{ .oidc.storageAccount.name }}'
param afdOidcBaseEndpoint = 'https://{{ .dns.regionalSubdomain }}.{{ .oidc.frontdoor.subdomain }}.{{ .dns.svcParentZoneName }}/'
