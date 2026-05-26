using '../templates/e2e-subscription-rbac-assignments.bicep'

param customerSubscriptionIds = [
  '{{ (index .devCi.e2eSubscriptionRbac.customerSubscriptions 0).id }}'
  '{{ (index .devCi.e2eSubscriptionRbac.customerSubscriptions 1).id }}'
]

param homeSubscriptionId = '{{ .devCi.e2eSubscriptionRbac.homeSubscription.id }}'

param firstPartyPrincipalId = '{{ .devCi.e2eSubscriptionRbac.sharedPrincipals.firstParty.principalId }}'

param armHelperPrincipalId = '{{ .devCi.e2eSubscriptionRbac.sharedPrincipals.armHelper.principalId }}'

param miMockPrincipalId = '{{ .devCi.e2eSubscriptionRbac.sharedPrincipals.miMock.principalId }}'

param msiMockPoolPrincipals = [
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-0'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-0").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-1'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-1").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-2'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-2").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-3'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-3").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-4'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-4").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-5'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-5").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-6'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-6").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-7'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-7").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-8'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-8").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-9'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-9").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-10'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-10").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-11'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-11").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-12'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-12").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-13'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-13").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-14'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-14").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-15'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-15").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-16'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-16").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-17'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-17").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-18'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-18").principalId }}'
  }
  {
    name: 'aro-hcp-msi-mock-cs-sp-dev-19'
    principalId: '{{ (index .devCi.e2eSubscriptionRbac.msiMockPool.principals "aro-hcp-msi-mock-cs-sp-dev-19").principalId }}'
  }
]

// Temporary adoption shim for the first CI-enabled E2E subscription.
// These role assignments already exist with Azure-generated GUID names from the
// old imperative flow. ARM/Bicep requires the role assignment resource name to
// be that GUID, so we must preserve the existing IDs until a one-time migration
// can recreate the assignments under pipeline-managed IDs.
param legacyAssignmentIdsBySubscription = {
  '974ebd46-8ad3-41e3-afef-7ef25fd5c371': {
    firstPartyRoleAssignment: '9d5c0406-7a07-44ad-99e7-141884735981'
    armHelperContributorRoleAssignment: 'e1981091-c0fa-4203-ad9c-68285e7f0377'
    armHelperRbacAdminRoleAssignment: '93a6eec1-1fa3-4238-995c-479b56b114b7'
    miMockRoleAssignment: '90929cfa-9556-4418-8324-18d0a17889d5'
    miMockKmsRoleAssignment: '6793a997-8733-462c-a153-76e8846b39b2'
    pooledMiMockRoleAssignments: {
      'aro-hcp-msi-mock-cs-sp-dev-0': '2746f4b6-af00-4f36-81e9-7119045eb6f8'
      'aro-hcp-msi-mock-cs-sp-dev-1': 'c77b5f83-4c90-4651-b316-d037e4adcf9d'
      'aro-hcp-msi-mock-cs-sp-dev-2': '969e11e0-ecd0-4b68-b757-d21d0469cad5'
      'aro-hcp-msi-mock-cs-sp-dev-3': '254e2c84-5b90-4de5-b585-4ca7da417570'
      'aro-hcp-msi-mock-cs-sp-dev-4': '7851b89e-dd0c-41c3-97c3-0a00174c0716'
      'aro-hcp-msi-mock-cs-sp-dev-5': '19b6611e-13f3-45b0-bacd-1f102124992d'
      'aro-hcp-msi-mock-cs-sp-dev-6': 'a6bc5dff-bc04-46b0-a45e-94d4bc5b4a64'
      'aro-hcp-msi-mock-cs-sp-dev-7': '00e0366a-f5c1-4543-9ea5-b595bc2d63b6'
      'aro-hcp-msi-mock-cs-sp-dev-8': '0a62414b-51cf-4551-9b55-b322e5d4669d'
      'aro-hcp-msi-mock-cs-sp-dev-9': '44699f1c-18c5-40e1-8ff7-f345ef791ef3'
      'aro-hcp-msi-mock-cs-sp-dev-10': '028e4d25-1583-4639-9182-b9bc78a304f6'
      'aro-hcp-msi-mock-cs-sp-dev-11': '9dcaca83-a773-4b16-9896-d3581e293f3f'
      'aro-hcp-msi-mock-cs-sp-dev-12': '0240dd76-068c-461a-9419-fcb3adc3d312'
      'aro-hcp-msi-mock-cs-sp-dev-13': '0a8bf640-5ba0-4dc4-ba49-453f9b2b9847'
      'aro-hcp-msi-mock-cs-sp-dev-14': '996ead14-39ab-4d9f-b3c9-503c40244d27'
      'aro-hcp-msi-mock-cs-sp-dev-15': '3137ba76-a5ec-4aa7-b3e4-719ce0fc3faf'
      'aro-hcp-msi-mock-cs-sp-dev-16': 'e6a24cc4-9b73-4be5-be17-3bd93d8ab31b'
      'aro-hcp-msi-mock-cs-sp-dev-17': '4fa5c0b7-e563-4fa9-8c59-59004896971f'
      'aro-hcp-msi-mock-cs-sp-dev-18': '20e5279e-b612-4fb0-a8cd-f19f96394a3b'
      'aro-hcp-msi-mock-cs-sp-dev-19': '84aaef29-455a-4a2b-b59a-ad8e4c3994e0'
    }
    pooledMiMockKmsRoleAssignments: {
      'aro-hcp-msi-mock-cs-sp-dev-0': 'd94ad61e-1a8d-4be0-bcf7-9baa8def1928'
      'aro-hcp-msi-mock-cs-sp-dev-1': '4b29de14-ec1a-4ce7-b44f-f9d1d1c43601'
      'aro-hcp-msi-mock-cs-sp-dev-2': '575e5a14-ba12-48bb-b11b-f562a8cce1fc'
      'aro-hcp-msi-mock-cs-sp-dev-3': '3b1ab32b-658a-4dc0-980a-08ba5caef900'
      'aro-hcp-msi-mock-cs-sp-dev-4': '2e1c7766-b02c-4ba5-bae5-2f4256d4bf7e'
      'aro-hcp-msi-mock-cs-sp-dev-5': '9c19607b-6971-4485-b7e7-528a5493a095'
      'aro-hcp-msi-mock-cs-sp-dev-6': '20238dae-9350-4acb-a9e6-ecba44b2aba6'
      'aro-hcp-msi-mock-cs-sp-dev-7': '010b66d4-7b64-45d6-aaf6-6eec01eb36c9'
      'aro-hcp-msi-mock-cs-sp-dev-8': '15c23298-102d-4d05-94ad-21bbbc616d0a'
      'aro-hcp-msi-mock-cs-sp-dev-9': 'daebbb95-4e38-4a65-8824-15e9392ac085'
      'aro-hcp-msi-mock-cs-sp-dev-10': 'bef62727-11c9-4743-aed6-5ba11db6166d'
      'aro-hcp-msi-mock-cs-sp-dev-11': 'd48a0c45-763f-47c1-881d-7e674efc0430'
      'aro-hcp-msi-mock-cs-sp-dev-12': 'ff20d40e-fd80-488c-8fe6-a9bad0ef8465'
      'aro-hcp-msi-mock-cs-sp-dev-13': '3fefb8eb-297e-4d4c-8bb1-b8126ff99ccd'
      'aro-hcp-msi-mock-cs-sp-dev-14': 'd9861ed0-e494-40c1-902c-ed2f8f9d7c90'
      'aro-hcp-msi-mock-cs-sp-dev-15': 'b5f455e6-669e-43b7-aef6-0b8515a872de'
      'aro-hcp-msi-mock-cs-sp-dev-16': 'f9d749a2-5558-4ad6-a01d-4584f6e40e2a'
      'aro-hcp-msi-mock-cs-sp-dev-17': '84b2d4ec-3a01-4113-9705-ebd45061cbb2'
      'aro-hcp-msi-mock-cs-sp-dev-18': 'f0c7b6ec-23e5-4a94-b1d6-75b3f92dbaab'
      'aro-hcp-msi-mock-cs-sp-dev-19': 'b390931e-400c-473d-8234-1293a46b4c81'
    }
  }
}
