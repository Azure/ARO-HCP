using '../modules/fleet/fleet-lookup.bicep'

param useLeasedInfrastructureIdentities = {{ .infrastructureIdentities.useLeased }}
param infrastructureIdentityResourceGroup = '{{ .infrastructureIdentities.serviceResourceGroup }}'

param msiName = '{{ .fleet.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
param regionalResourceGroup = '{{ .regionRG }}'
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}'
param cxDnsZoneName = '{{ .dns.regionalSubdomain }}.{{ .dns.cxParentZoneName }}'
param svcMonitorName = '{{ .monitoring.svcWorkspaceName }}'
param hcpMonitorName = '{{ .monitoring.hcpWorkspaceName }}'
