// https://learn.microsoft.com/en-us/azure/reliability/availability-zones-region-support
// See helper script in dev-infrastructure/scripts/list-az-locations.sh
var _zoneRedundantLocations = [
  'australiaeast'
  'brazilsouth'
  'canadacentral'
  'centralindia'
  'centralus'
  'centraluseuap'
  'eastasia'
  'eastus'
  'eastus2'
  'eastus2euap'
  'francecentral'
  'germanywestcentral'
  'israelcentral'
  'italynorth'
  'japaneast'
  'koreacentral'
  'mexicocentral'
  'newzealandnorth'
  'northeurope'
  'norwayeast'
  'polandcentral'
  'qatarcentral'
  'southafricanorth'
  'southcentralus'
  'southeastasia'
  'spaincentral'
  'swedencentral'
  'switzerlandnorth'
  'uaenorth'
  'uksouth'
  'westeurope'
  'westus2'
  'westus3'

  // The following regions do not support availability zones
  // asia
  // asiapacific
  // australia
  // australiacentral
  // australiacentral2
  // australiasoutheast
  // brazil
  // brazilsoutheast
  // brazilus
  // canada
  // canadaeast
  // centralusstage
  // eastasiastage
  // eastus2stage
  // eastusstage
  // eastusstg
  // europe
  // france
  // francesouth
  // germany
  // germanynorth
  // global
  // india
  // israel
  // italy
  // japan
  // japanwest
  // jioindiacentral
  // jioindiawest
  // korea
  // koreasouth
  // newzealand
  // northcentralus
  // northcentralusstage
  // norway
  // norwaywest
  // poland
  // qatar
  // singapore
  // southafrica
  // southafricawest
  // southcentralusstage
  // southcentralusstg
  // southeastasiastage
  // southindia
  // sweden
  // switzerland
  // switzerlandwest
  // uae
  // uaecentral
  // uk
  // ukwest
  // unitedstates
  // unitedstateseuap
  // westcentralus
  // westindia
  // westus
  // westus2stage
  // westusstage
]

@export()
func locationIsZoneRedundant(region string) bool => contains(_zoneRedundantLocations, region)
