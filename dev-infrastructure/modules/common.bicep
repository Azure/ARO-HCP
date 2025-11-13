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
    availabilityZones: []
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
      // '2' Not available in EV2
      '3'
      '4'
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

var _regionToGeography = {
  apacsoutheast2: {
    geoShortId: 'ap'
    geography: 'Asia Pacific'
  }
  australiacentral: {
    geoShortId: 'au'
    geography: 'Australia'
  }
  australiacentral2: {
    geoShortId: 'au'
    geography: 'Australia'
  }
  australiaeast: {
    geoShortId: 'au'
    geography: 'Australia'
  }
  australiasoutheast: {
    geoShortId: 'au'
    geography: 'Australia'
  }
  austriaeast: {
    geoShortId: 'at'
    geography: 'Austria'
  }
  belgiumcentral: {
    geoShortId: 'be'
    geography: 'Belgium'
  }
  brazilnortheast: {
    geoShortId: 'br'
    geography: 'Brazil'
  }
  brazilsouth: {
    geoShortId: 'br'
    geography: 'Brazil'
  }
  brazilsoutheast: {
    geoShortId: 'br'
    geography: 'Brazil'
  }
  canadacentral: {
    geoShortId: 'ca'
    geography: 'Canada'
  }
  canadaeast: {
    geoShortId: 'ca'
    geography: 'Canada'
  }
  centralindia: {
    geoShortId: 'in'
    geography: 'India'
  }
  centralus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  centraluseuap: {
    geoShortId: 'usc'
    geography: 'Canary (US)'
  }
  chilecentral: {
    geoShortId: 'cl'
    geography: 'Chile'
  }
  denmarkeast: {
    geoShortId: 'dk'
    geography: 'Denmark'
  }
  eastasia: {
    geoShortId: 'ap'
    geography: 'Asia Pacific'
  }
  eastus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  eastus2: {
    geoShortId: 'us'
    geography: 'United States'
  }
  eastus2euap: {
    geoShortId: 'usc'
    geography: 'Canary (US)'
  }
  eastus3: {
    geoShortId: 'us'
    geography: 'United States'
  }
  eastusslv: {
    geoShortId: 'ust'
    geography: 'Stage (US)'
  }
  eastusstg: {
    geoShortId: 'ust'
    geography: 'Stage (US)'
  }
  francecentral: {
    geoShortId: 'fr'
    geography: 'France'
  }
  francesouth: {
    geoShortId: 'fr'
    geography: 'France'
  }
  germanynorth: {
    geoShortId: 'de'
    geography: 'Germany'
  }
  germanywestcentral: {
    geoShortId: 'de'
    geography: 'Germany'
  }
  indiasouthcentral: {
    geoShortId: 'in'
    geography: 'India'
  }
  indonesiacentral: {
    geoShortId: 'id'
    geography: 'Indonesia'
  }
  israelcentral: {
    geoShortId: 'il'
    geography: 'Israel'
  }
  israelnorthwest: {
    geoShortId: 'il'
    geography: 'Israel'
  }
  italynorth: {
    geoShortId: 'it'
    geography: 'Italy'
  }
  japaneast: {
    geoShortId: 'jp'
    geography: 'Japan'
  }
  japanwest: {
    geoShortId: 'jp'
    geography: 'Japan'
  }
  jioindiacentral: {
    geoShortId: 'in'
    geography: 'India'
  }
  jioindiawest: {
    geoShortId: 'in'
    geography: 'India'
  }
  koreacentral: {
    geoShortId: 'kr'
    geography: 'Korea'
  }
  koreasouth: {
    geoShortId: 'kr'
    geography: 'Korea'
  }
  koreasouth2: {
    geoShortId: 'kr'
    geography: 'Korea'
  }
  malaysiasouth: {
    geoShortId: 'my'
    geography: 'Malaysia'
  }
  malaysiawest: {
    geoShortId: 'my'
    geography: 'Malaysia'
  }
  mexicocentral: {
    geoShortId: 'mx'
    geography: 'Mexico'
  }
  newzealandnorth: {
    geoShortId: 'nz'
    geography: 'New Zealand'
  }
  northcentralus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  northeastus5: {
    geoShortId: 'us'
    geography: 'United States'
  }
  northeurope: {
    geoShortId: 'eu'
    geography: 'Europe'
  }
  norwayeast: {
    geoShortId: 'no'
    geography: 'Norway'
  }
  norwaywest: {
    geoShortId: 'no'
    geography: 'Norway'
  }
  polandcentral: {
    geoShortId: 'pl'
    geography: 'Poland'
  }
  qatarcentral: {
    geoShortId: 'qa'
    geography: 'Qatar'
  }
  southafricanorth: {
    geoShortId: 'za'
    geography: 'South Africa'
  }
  southafricawest: {
    geoShortId: 'za'
    geography: 'South Africa'
  }
  southcentralus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  southcentralus2: {
    geoShortId: 'us'
    geography: 'United States'
  }
  southcentralusstg: {
    geoShortId: 'ust'
    geography: 'Stage (US)'
  }
  southeastasia: {
    geoShortId: 'ap'
    geography: 'Asia Pacific'
  }
  southeastasia3: {
    geoShortId: 'my'
    geography: 'Malaysia'
  }
  southeastus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  southeastus3: {
    geoShortId: 'us'
    geography: 'United States'
  }
  southeastus5: {
    geoShortId: 'us'
    geography: 'United States'
  }
  southindia: {
    geoShortId: 'in'
    geography: 'India'
  }
  southwestus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  spaincentral: {
    geoShortId: 'es'
    geography: 'Spain'
  }
  swedencentral: {
    geoShortId: 'se'
    geography: 'Sweden'
  }
  swedensouth: {
    geoShortId: 'se'
    geography: 'Sweden'
  }
  switzerlandnorth: {
    geoShortId: 'ch'
    geography: 'Switzerland'
  }
  switzerlandwest: {
    geoShortId: 'ch'
    geography: 'Switzerland'
  }
  taiwannorth: {
    geoShortId: 'tw'
    geography: 'Taiwan'
  }
  taiwannorthwest: {
    geoShortId: 'tw'
    geography: 'Taiwan'
  }
  uaecentral: {
    geoShortId: 'ae'
    geography: 'UAE'
  }
  uaenorth: {
    geoShortId: 'ae'
    geography: 'UAE'
  }
  uksouth: {
    geoShortId: 'uk'
    geography: 'United Kingdom'
  }
  ukwest: {
    geoShortId: 'uk'
    geography: 'United Kingdom'
  }
  westcentralus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  westcentralusfre: {
    geoShortId: 'us'
    geography: 'United States'
  }
  westeurope: {
    geoShortId: 'eu'
    geography: 'Europe'
  }
  westindia: {
    geoShortId: 'in'
    geography: 'India'
  }
  westus: {
    geoShortId: 'us'
    geography: 'United States'
  }
  westus2: {
    geoShortId: 'us'
    geography: 'United States'
  }
  westus3: {
    geoShortId: 'us'
    geography: 'United States'
  }
}

@export()
func splitOrEmptyArray(inputString string, delimiter string) array =>
  inputString == '' || inputString == null ? [] : split(inputString, delimiter)

@export()
func csvToArray(inputString string) array => splitOrEmptyArray(inputString, ',')

@export()
func arrayToCSV(inputArray array) string => join(inputArray, ',')

@export()
func getLocationAvailabilityZones(region string) array => _locationAvailabilityZones[region].availabilityZones

@export()
func getLocationAvailabilityZonesCSV(region string) string => arrayToCSV(getLocationAvailabilityZones(region))

@export()
func getGeoShortForRegion(region string) string => _regionToGeography[region].geoShortId

@export()
func determineZoneRedundancyForRegion(region string, mode string) bool =>
  determineZoneRedundancy(getLocationAvailabilityZones(region), mode)

@export()
func determineZoneRedundancy(availabilityZones array, mode string) bool =>
  mode == 'Auto' ? length(availabilityZones) > 0 : mode == 'Enabled' && length(availabilityZones) > 0

@export()
func generateZoneList(count int) array => count > 0 ? map(range(1, count), i => string(i)) : []

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

// Function to safely truncate strings, ensuring no trailing dashes or problematic characters
@export()
func safeTake(input string, maxLength int) string =>
  length(take(input, maxLength)) > 0 && (endsWith(take(input, maxLength), '-') || endsWith(take(input, maxLength), '_') || endsWith(
      take(input, maxLength),
      '.'
    ))
    ? take(take(input, maxLength), length(take(input, maxLength)) - 1)
    : take(input, maxLength)
