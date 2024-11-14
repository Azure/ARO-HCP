// copy from dev-infrastructure/configurations/region.bicepparam
using '../templates/region.bicep'

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// CS
param csImage = '__CLUSTERSERVICE_IMAGETAG__'
param regionRG = '__REGIONRG__'
