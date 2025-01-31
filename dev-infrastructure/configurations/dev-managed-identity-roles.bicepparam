using '../templates/dev-managed-identity-roles.bicep'

param roles = [
  {
    roleName: 'Azure Red Hat OpenShift Cloud Controller Manager - Dev'
    roleDescription: 'Enables permissions for the operator to manage and update the cloud controller managers deployed on top of OpenShift.'
    actions: [
        'Microsoft.Network/virtualNetworks/read',
        'Microsoft.Network/virtualNetworks/subnets/read',
        'Microsoft.Network/virtualNetworks/subnets/write',

        'Microsoft.Network/routeTables/read',
        'Microsoft.Network/routeTables/join/action',

        'Microsoft.Network/natGateways/join/action',
        'Microsoft.Network/natGateways/read',

        'Microsoft.Network/networkSecurityGroups/read',
        'Microsoft.Network/networkSecurityGroups/join/action',

        'Microsoft.ManagedIdentity/userAssignedIdentities/read',
        'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write',
        'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read',
        'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
]
