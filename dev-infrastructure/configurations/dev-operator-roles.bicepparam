using '../templates/dev-operator-roles.bicep'

param roles = [
  {
    roleName: 'Azure Red Hat OpenShift Cloud Controller Manager - Dev'
    roleDescription: 'Enables permissions for the operator to manage and update the cloud controller managers deployed on top of OpenShift.'
    actions: [
      'Microsoft.Compute/virtualMachines/read'
      'Microsoft.Network/loadBalancers/backendAddressPools/join/action'
      'Microsoft.Network/loadBalancers/read'
      'Microsoft.Network/loadBalancers/write'
      'Microsoft.Network/loadBalancers/inboundNatRules/join/action'
      'Microsoft.Network/networkInterfaces/read'
      'Microsoft.Network/networkInterfaces/write'
      'Microsoft.Network/networkSecurityGroups/read'
      'Microsoft.Network/networkSecurityGroups/write'
      'Microsoft.Network/networkSecurityGroups/join/action'
      'Microsoft.Network/publicIPAddresses/join/action'
      'Microsoft.Network/publicIPAddresses/read'
      'Microsoft.Network/publicIPAddresses/write'
      'Microsoft.Network/virtualNetworks/subnets/join/action'
      'Microsoft.Network/virtualNetworks/subnets/read'
      'Microsoft.Network/publicIPPrefixes/join/action'
      'Microsoft.Network/applicationSecurityGroups/joinNetworkSecurityRule/action'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift Cluster Ingress Operator - Dev'
    roleDescription: 'Enables permissions for the operator to configure and manage the OpenShift router.'
    actions: [
      'Microsoft.Network/dnsZones/A/delete'
      'Microsoft.Network/dnsZones/A/write'
      'Microsoft.Network/privateDnsZones/A/delete'
      'Microsoft.Network/privateDnsZones/A/write'
      'Microsoft.Network/virtualNetworks/subnets/read'
      'Microsoft.Network/virtualNetworks/subnets/join/action'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift Disk Storage Operator - Dev'
    roleDescription: 'Enables permissions to set OpenShift cluster-wide storage defaults. It ensures a default storageclass exists for clusters. It also installs Container Storage Interface (CSI) drivers which enable your cluster to use various storage backends.'
    actions: [
      'Microsoft.Compute/virtualMachines/write'
      'Microsoft.Compute/virtualMachines/read'
      'Microsoft.Compute/virtualMachineScaleSets/virtualMachines/write'
      'Microsoft.Compute/virtualMachineScaleSets/virtualMachines/read'
      'Microsoft.Compute/virtualMachineScaleSets/read'
      'Microsoft.Compute/snapshots/write'
      'Microsoft.Compute/snapshots/read'
      'Microsoft.Compute/snapshots/delete'
      'Microsoft.Compute/locations/operations/read'
      'Microsoft.Compute/locations/DiskOperations/read'
      'Microsoft.Compute/disks/write'
      'Microsoft.Compute/disks/read'
      'Microsoft.Compute/disks/delete'
      'Microsoft.Compute/disks/beginGetAccess/action'
      'Microsoft.Resources/subscriptions/resourceGroups/read'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift File Storage Operator - Dev'
    roleDescription: 'Enables permissions to set OpenShift cluster-wide storage defaults. It ensures a default storageclass exists for clusters. It also installs Container Storage Interface (CSI) drivers which enable your cluster to use Azure Files.'
    actions: [
      'Microsoft.Compute/diskEncryptionSets/read'
      'Microsoft.Storage/storageAccounts/delete'
      'Microsoft.Storage/storageAccounts/fileServices/read'
      'Microsoft.Storage/storageAccounts/fileServices/shares/delete'
      'Microsoft.Storage/storageAccounts/fileServices/shares/read'
      'Microsoft.Storage/storageAccounts/fileServices/shares/write'
      'Microsoft.Storage/storageAccounts/listKeys/action'
      'Microsoft.Storage/storageAccounts/read'
      'Microsoft.Storage/storageAccounts/write'
      'Microsoft.Network/networkSecurityGroups/join/action'
      'Microsoft.Network/virtualNetworks/subnets/read'
      'Microsoft.Network/virtualNetworks/subnets/write'
      'Microsoft.Network/routeTables/join/action'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift Network Operator - Dev'
    roleDescription: 'Enables permissions to install and upgrade the networking components on an OpenShift cluster.'
    actions: [
      'Microsoft.Network/networkInterfaces/read'
      'Microsoft.Network/networkInterfaces/write'
      'Microsoft.Network/virtualNetworks/read'
      'Microsoft.Network/virtualNetworks/subnets/join/action'
      'Microsoft.Network/loadBalancers/backendAddressPools/join/action'
      'Microsoft.Compute/virtualMachines/read'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift Image Registry Operator - Dev'
    roleDescription: 'Enables permissions for the operator to manage a singleton instance of the OpenShift image registry. It manages all configuration of the registry including creating storage.'
    actions: [
      'Microsoft.Storage/storageAccounts/blobServices/read'
      'Microsoft.Storage/storageAccounts/blobServices/containers/delete'
      'Microsoft.Storage/storageAccounts/blobServices/containers/read'
      'Microsoft.Storage/storageAccounts/blobServices/containers/write'
      'Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey/action'
      'Microsoft.Storage/storageAccounts/read'
      'Microsoft.Storage/storageAccounts/write'
      'Microsoft.Storage/storageAccounts/delete'
      'Microsoft.Storage/storageAccounts/listKeys/action'
      'Microsoft.Resources/tags/write'
      'Microsoft.Network/privateEndpoints/write'
      'Microsoft.Network/privateEndpoints/read'
      'Microsoft.Network/privateEndpoints/privateDnsZoneGroups/write'
      'Microsoft.Network/privateEndpoints/privateDnsZoneGroups/read'
      'Microsoft.Network/privateDnsZones/read'
      'Microsoft.Network/privateDnsZones/write'
      'Microsoft.Network/privateDnsZones/join/action'
      'Microsoft.Network/privateDnsZones/A/write'
      'Microsoft.Network/privateDnsZones/virtualNetworkLinks/write'
      'Microsoft.Network/privateDnsZones/virtualNetworkLinks/read'
      'Microsoft.Network/networkInterfaces/read'
      'Microsoft.Storage/storageAccounts/PrivateEndpointConnectionsApproval/action'
      'Microsoft.Network/virtualNetworks/subnets/read'
      'Microsoft.Network/virtualNetworks/subnets/join/action'
      'Microsoft.Network/virtualNetworks/join/action'
    ]
    notActions: []
    dataActions: [
      'Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete'
      'Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write'
      'Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read'
      'Microsoft.Storage/storageAccounts/blobServices/containers/blobs/add/action'
      'Microsoft.Storage/storageAccounts/blobServices/containers/blobs/move/action'
    ]
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift Cluster API Role - Dev'
    roleDescription: 'Enables permissions to allow cluster API to manage nodes, networks and disks for OpenShift cluster.'
    actions: [
      'Microsoft.Compute/availabilitySets/delete'
      'Microsoft.Compute/availabilitySets/read'
      'Microsoft.Compute/availabilitySets/write'
      'Microsoft.Compute/disks/delete'
      'Microsoft.Compute/disks/read'
      'Microsoft.Compute/disks/write'
      'Microsoft.Compute/virtualMachines/delete'
      'Microsoft.Compute/virtualMachines/read'
      'Microsoft.Compute/virtualMachines/write'
      'Microsoft.Network/loadBalancers/backendAddressPools/join/action'
      'Microsoft.Network/networkInterfaces/delete'
      'Microsoft.Network/networkInterfaces/join/action'
      'Microsoft.Network/networkInterfaces/read'
      'Microsoft.Network/networkInterfaces/write'
      'Microsoft.Network/virtualNetworks/subnets/join/action'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift Control Plane Operator Role - Dev'
    roleDescription: 'Enables the control plane operator to read resources necessary for OpenShift cluster.'
    actions: [
      'Microsoft.Resources/subscriptions/resourceGroups/read'
      'Microsoft.Network/virtualNetworks/read'
      'Microsoft.Network/networkSecurityGroups/read'
    ]
    notActions: []
    dataActions: []
    notDataActions: []
  }
  {
    roleName: 'Azure Red Hat OpenShift KMS Plugin - Dev'
    roleDescription: 'Enables permissions for the apiserver encryption plugin to access the Azure KeyVault instance from the OpenShift cluster.'
    actions: []
    notActions: []
    dataActions: [
      'Microsoft.KeyVault/vaults/keys/read'
      'Microsoft.KeyVault/vaults/keys/update/action'
      'Microsoft.KeyVault/vaults/keys/backup/action'
      'Microsoft.KeyVault/vaults/keys/encrypt/action'
      'Microsoft.KeyVault/vaults/keys/decrypt/action'
      'Microsoft.KeyVault/vaults/keys/wrap/action'
      'Microsoft.KeyVault/vaults/keys/unwrap/action'
      'Microsoft.KeyVault/vaults/keys/sign/action'
      'Microsoft.KeyVault/vaults/keys/verify/action'
    ]
    notDataActions: []
  }
]
