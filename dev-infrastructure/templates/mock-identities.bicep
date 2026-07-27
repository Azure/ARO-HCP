@description('Global resource group name')
param globalResourceGroupName string = 'global'

@description('The name of the first party identity role')
param firstPartyRoleName string = 'dev-first-party-mock'

@description('The name of the msi mock identity role')
param msiMockRoleName string = 'dev-msi-mock'

@description('E2E Test subscription ID, that needs to use this role as well')
param e2eTestSubscription string

// NOTE: The mock identity certificates are no longer created here. Bicep cannot
// create Key Vault certificates, which previously required a
// Microsoft.Resources/deploymentScripts resource (key-vault-cert.bicep). That
// deploymentScript has been removed (ARO-28515); the self-signed mock
// certificates are now provisioned by scripts/create-kv-cert.sh, invoked from
// the dev-infrastructure Makefile targets before the service principals are
// created from them.

//
// F I R S T   P A R T Y   I D E N T I T Y
//

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
// M S I   R P   M O C K   I D E N T I T Y
//

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
          'Microsoft.Compute/diskEncryptionSets/read' // validate DES if provided
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
