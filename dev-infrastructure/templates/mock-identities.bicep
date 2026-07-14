@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the MSI used for Key Vault operations')
param globalMSIName string

@description('The name of the key vault')
param keyVaultName string

@description('Global resource group name')
param globalResourceGroupName string = 'global'

@description('The name of the first party identity role')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('The name of the first party certificate')
param firstPartyCertName string = 'firstPartyCert2'

@description('The DNS of the first party certificate, used for subject and DNS names.')
param firstPartyCertDns string = 'firstparty.hcp.osadev.cloud'

@description('The name of the msi mock identity role')
param msiMockRoleName string = 'dev-msi-mock'

@description('The name of the msi mock certificate')
param msiMockCertName string = 'msiMockCert2'

@description('The DNS of the msi mock certificate, used for subject and DNS names.')
param msiMockCertDns string = 'msimock.hcp.osadev.cloud'

@description('Number of additional MSI mock identities to create for throttle distribution')
param msiMockPoolSize int = 0

@description('Base name for pooled MSI mock certificates')
param msiMockPoolCertBaseName string = 'msiMockPoolCert'

@description('Base DNS for pooled MSI mock certificates')
param msiMockPoolCertBaseDns string = 'msimockpool.hcp.osadev.cloud'

@description('The name of the arm helper mock certificate')
param armHelperCertName string = 'armHelperCert2'

@description('The DNS of the arm helper mock certificate, used for subject and DNS names.')
param armHelperCertDns string = 'armhelper.hcp.osadev.cloud'

@description('E2E Test subscription ID, that needs to use this role as well')
param e2eTestSubscription string

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

//
// F I R S T   P A R T Y   I D E N T I T Y
//

module firstPartyIdentity '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'first-party-identity'
  params: {
    location: location
    keyVaultManagedIdentityId: globalMSI.id
    keyVaultName: keyVaultName
    certName: firstPartyCertName
    subjectName: 'CN=${firstPartyCertDns}'
    issuerName: 'Self'
    dnsNames: [firstPartyCertDns]
    validityInMonths: 120
  }
}

resource customRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, firstPartyRoleName)
  properties: {
    roleName: firstPartyRoleName
    description: 'ARO HCP Dev Role for mock 1p service principal'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/delete'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/write'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/read'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/details/read'
          'Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/validate/action'
          'Microsoft.Resources/subscriptions/resourceGroups/read'
          'Microsoft.Resources/subscriptions/resourceGroups/write'
        ]
        notActions: []
      }
    ]
    assignableScopes: [
      subscription().id
      subscriptionResourceId('Microsoft.Resources/resourceGroups/', globalResourceGroupName)
      '/subscriptions/${e2eTestSubscription}'
    ]
  }
}

//
// A R M   H E L P E R   I D E N T I T Y
//

module armHelperIdentity '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'arm-helper-identity'
  params: {
    location: location
    keyVaultManagedIdentityId: globalMSI.id
    keyVaultName: keyVaultName
    certName: armHelperCertName
    subjectName: 'CN=${armHelperCertDns}'
    dnsNames: [armHelperCertDns]
    issuerName: 'Self'
    validityInMonths: 120
  }
}

//
// M S I   R P   M O CK   I D E N T I T Y
//

module msiRPMockIdentity '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'msi-mock-identity'
  params: {
    location: location
    keyVaultManagedIdentityId: globalMSI.id
    keyVaultName: keyVaultName
    certName: msiMockCertName
    subjectName: 'CN=${msiMockCertDns}'
    dnsNames: [msiMockCertDns]
    issuerName: 'Self'
    validityInMonths: 120
  }
}

//
// M S I   R P   M O C K   I D E N T I T Y   P O O L
//
// Additional MSI mock identities to distribute ARM read load across multiple
// service principals, avoiding per-principal subscription-level throttling
// during concurrent E2E test runs.

module msiRPMockIdentityPool '../modules/keyvault/key-vault-cert.bicep' = [
  for i in range(0, msiMockPoolSize): {
    name: 'msi-mock-identity-pool-${i}'
    dependsOn: [msiRPMockIdentity]
    params: {
      location: location
      keyVaultManagedIdentityId: globalMSI.id
      keyVaultName: keyVaultName
      certName: '${msiMockPoolCertBaseName}-${i}'
      subjectName: 'CN=${i}.${msiMockPoolCertBaseDns}'
      dnsNames: ['${i}.${msiMockPoolCertBaseDns}']
      issuerName: 'Self'
      validityInMonths: 120
    }
  }
]

resource msiCustomRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, msiMockRoleName)
  properties: {
    roleName: msiMockRoleName
    description: 'ARO HCP Dev Role for MSI mock principal'
    type: 'CustomRole'
    permissions: [
      {
        actions: [
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read'
          'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write'
          'Microsoft.ManagedIdentity/userAssignedIdentities/read'
          'Microsoft.ManagedIdentity/userAssignedIdentities/assign/action' // attach customer-provided MIs (e.g. ACR pull) to VMs via CAPZ
          'Microsoft.Network/loadBalancers/backendAddressPools/read' // read backend address pools of LB to check if the backend address pool already exists
          'Microsoft.Network/loadBalancers/backendAddressPools/write' // write backend address pools to LB
          'Microsoft.Network/loadBalancers/read' // to check if LB exists or not before writing to it
          'Microsoft.Network/loadBalancers/write' // create LB if it doesn't exist
          'Microsoft.Network/natGateways/join/action' // subnet/write needs /join/action on nat gateway if present in request
          'Microsoft.Network/natGateways/read'
          'Microsoft.Network/networkSecurityGroups/join/action' // subnet/write needs /join/action on NSG if present in request
          'Microsoft.Network/networkSecurityGroups/read' // validate NSG 
          'Microsoft.Network/networkSecurityGroups/write'
          'Microsoft.Network/privateDnsZones/virtualNetworkLinks/read' // read existing links between private DNS zone and virtual network
          'Microsoft.Network/privateDnsZones/virtualNetworkLinks/write' // attach private DNS zone to virtual network
          'Microsoft.Network/routeTables/join/action' // subnet/write needs /join/action on nat route table if present in request
          'Microsoft.Network/routeTables/read'
          'Microsoft.Network/virtualNetworks/join/action' // attach private DNS zone
          'Microsoft.Network/virtualNetworks/joinLoadBalancer/action' // add private IP addresses to LB backend
          'Microsoft.Network/virtualNetworks/read' // validate CIDR & existance
          'Microsoft.Network/virtualNetworks/subnets/join/action' // create private load balancer and join to subnet
          'Microsoft.Network/virtualNetworks/subnets/read' // validate CIDR & existance
          'Microsoft.Network/virtualNetworks/subnets/write' // attach the NSG to subnet
        ]
        notActions: []
      }
    ]
    assignableScopes: [
      subscription().id
      subscriptionResourceId('Microsoft.Resources/resourceGroups/', globalResourceGroupName)
      '/subscriptions/${e2eTestSubscription}'
    ]
  }
}
