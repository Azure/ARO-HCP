// https://learn.microsoft.com/en-us/azure/reliability/availability-zones-region-support
// See helper script in dev-infrastructure/scripts/list-az-locations.sh
var _locationAvailabilityZones = {
  asia: {
    availabilityZones: []
  }
  asiapacific: {
    availabilityZones: []
  }
  australia: {
    availabilityZones: []
  }
  australiacentral: {
    availabilityZones: []
  }
  australiacentral2: {
    availabilityZones: []
  }
  australiaeast: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  australiasoutheast: {
    availabilityZones: []
  }
  brazil: {
    availabilityZones: []
  }
  brazilsouth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  brazilsoutheast: {
    availabilityZones: []
  }
  brazilus: {
    availabilityZones: []
  }
  canada: {
    availabilityZones: []
  }
  canadacentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  canadaeast: {
    availabilityZones: []
  }
  centralindia: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  centralus: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  centraluseuap: {
    availabilityZones: [
      '1'
      '2'
    ]
  }
  centralusstage: {
    availabilityZones: []
  }
  eastasia: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  eastasiastage: {
    availabilityZones: []
  }
  eastus: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  eastus2: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  eastus2euap: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  eastus2stage: {
    availabilityZones: []
  }
  eastusstage: {
    availabilityZones: []
  }
  eastusstg: {
    availabilityZones: []
  }
  europe: {
    availabilityZones: []
  }
  france: {
    availabilityZones: []
  }
  francecentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  francesouth: {
    availabilityZones: []
  }
  germany: {
    availabilityZones: []
  }
  germanynorth: {
    availabilityZones: []
  }
  germanywestcentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  global: {
    availabilityZones: []
  }
  india: {
    availabilityZones: []
  }
  israel: {
    availabilityZones: []
  }
  israelcentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  italy: {
    availabilityZones: []
  }
  italynorth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  japan: {
    availabilityZones: []
  }
  japaneast: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  japanwest: {
    availabilityZones: []
  }
  jioindiacentral: {
    availabilityZones: []
  }
  jioindiawest: {
    availabilityZones: []
  }
  korea: {
    availabilityZones: []
  }
  koreacentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  koreasouth: {
    availabilityZones: []
  }
  mexicocentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  newzealand: {
    availabilityZones: []
  }
  newzealandnorth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  northcentralus: {
    availabilityZones: []
  }
  northcentralusstage: {
    availabilityZones: []
  }
  northeurope: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  norway: {
    availabilityZones: []
  }
  norwayeast: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  norwaywest: {
    availabilityZones: []
  }
  poland: {
    availabilityZones: []
  }
  polandcentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  qatar: {
    availabilityZones: []
  }
  qatarcentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  singapore: {
    availabilityZones: []
  }
  southafrica: {
    availabilityZones: []
  }
  southafricanorth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  southafricawest: {
    availabilityZones: []
  }
  southcentralus: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  southcentralusstage: {
    availabilityZones: []
  }
  southcentralusstg: {
    availabilityZones: []
  }
  southeastasia: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  southeastasiastage: {
    availabilityZones: []
  }
  southindia: {
    availabilityZones: []
  }
  spaincentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  sweden: {
    availabilityZones: []
  }
  swedencentral: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  switzerland: {
    availabilityZones: []
  }
  switzerlandnorth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  switzerlandwest: {
    availabilityZones: []
  }
  uae: {
    availabilityZones: []
  }
  uaecentral: {
    availabilityZones: []
  }
  uaenorth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  uk: {
    availabilityZones: []
  }
  uksouth: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  ukwest: {
    availabilityZones: []
  }
  unitedstates: {
    availabilityZones: []
  }
  unitedstateseuap: {
    availabilityZones: []
  }
  westcentralus: {
    availabilityZones: []
  }
  westeurope: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  westindia: {
    availabilityZones: []
  }
  westus: {
    availabilityZones: []
  }
  westus2: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  westus2stage: {
    availabilityZones: []
  }
  westus3: {
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  westusstage: {
    availabilityZones: []
  }
}

@export()
func csvToArray(inputString string) array => inputString == '' ? [] : split(inputString, ',')

@export()
func arrayToCSV(inputArray array) string => join(inputArray, ',')

@export()
func getLocationAvailabilityZones(region string) array => _locationAvailabilityZones[region].availabilityZones

@export()
func getLocationAvailabilityZonesCSV(region string) string => arrayToCSV(getLocationAvailabilityZones(region))

@export()
func determineZoneRedundancyForRegion(region string, mode string) bool =>
  determineZoneRedundancy(getLocationAvailabilityZones(region), mode)

@export()
func determineZoneRedundancy(availabilityZones array, mode string) bool =>
  mode == 'Auto' ? length(availabilityZones) > 0 : mode == 'Enabled' && length(availabilityZones) > 0

@export()
type IPServiceTag = {
  ipTagType: string
  tag: string
}

@export()
func parseIPServiceTag(tag string) IPServiceTag => {
  ipTagType: split(tag, ':')[0]
  tag: split(tag, ':')[1]
}
