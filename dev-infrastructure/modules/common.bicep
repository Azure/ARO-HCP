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
func determineZoneRedundancyForRegion(region string, mode string) bool =>
  determineZoneRedundancy(getLocationAvailabilityZones(region), mode)

@export()
func determineZoneRedundancy(availabilityZones array, mode string) bool =>
  mode == 'Auto' ? length(availabilityZones) > 0 : mode == 'Enabled' && length(availabilityZones) > 0

// Resolves a Postgres Flexible Server highAvailability.mode. 'Auto' picks
// 'ZoneRedundant' where the region has availability zones and 'SameZone'
// otherwise; the explicit modes ('Disabled', 'SameZone', 'ZoneRedundant') pass
// through unchanged so callers can pin a mode (e.g. 'Disabled' as the required
// intermediate step when migrating between 'SameZone' and 'ZoneRedundant').
@export()
func determinePostgresHAMode(region string, mode string) string =>
  mode == 'Auto' ? (length(getLocationAvailabilityZones(region)) > 0 ? 'ZoneRedundant' : 'SameZone') : mode

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

// Expects each CSV entry to be in 'key=value' format. Malformed entries
// (missing '=') will cause a runtime split-index error in Bicep.
@export()
func csvTagsToObject(tagsCSV string) object =>
  toObject(csvToArray(tagsCSV), tag => split(tag, '=')[0], tag => split(tag, '=')[1])

// Function to safely truncate strings, ensuring no trailing dashes or problematic characters
@export()
func safeTake(input string, maxLength int) string =>
  length(take(input, maxLength)) > 0 && (endsWith(take(input, maxLength), '-') || endsWith(take(input, maxLength), '_') || endsWith(
      take(input, maxLength),
      '.'
    ))
    ? take(take(input, maxLength), length(take(input, maxLength)) - 1)
    : take(input, maxLength)

// Highly privileged built-in role definition IDs that must never be sub-assigned
// by a constrained RBAC Administrator (Owner, User Access Administrator, RBAC Administrator).
var _ownerRoleDefinitionId = '8e3af657-a8ff-443c-a75c-2fe8c4bcb635'
var _userAccessAdminRoleDefinitionId = '18d7d88d-d35e-4fb5-a5c3-7773c20a72d9'
var _rbacAdminRoleDefinitionId = 'f58310d9-a9f6-439a-9e8d-f62e7b41a168'

// ABAC condition (v2.0) restricting a role assignment so the grantee can create/delete
// role assignments for any role EXCEPT the three privileged roles above. Shared verbatim
// by every constrained RBAC-Administrator grant so the guarded role set stays in sync.
@export()
var restrictedRoleAssignmentCondition = '((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/write\'})) OR (@Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${_ownerRoleDefinitionId}, ${_userAccessAdminRoleDefinitionId}, ${_rbacAdminRoleDefinitionId}})) AND ((!(ActionMatches{\'Microsoft.Authorization/roleAssignments/delete\'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {${_ownerRoleDefinitionId}, ${_userAccessAdminRoleDefinitionId}, ${_rbacAdminRoleDefinitionId}}))'

@export()
var restrictedRoleAssignmentConditionVersion = '2.0'
