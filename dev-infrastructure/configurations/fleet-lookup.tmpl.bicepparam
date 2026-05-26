using '../modules/fleet/fleet-lookup.bicep'

param msiName = '{{ .fleet.managedIdentityName }}'
param regionalResourceGroup = '{{ .regionRG }}'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param cxDnsZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.cxParentZoneName }}'
