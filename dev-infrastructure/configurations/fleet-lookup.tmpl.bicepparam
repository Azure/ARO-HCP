using '../modules/fleet/fleet-lookup.bicep'

param msiName = '{{ .fleet.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param regionalResourceGroup = '{{ .regionRG }}'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param cxDnsZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.cxParentZoneName }}'
