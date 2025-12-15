targetScope = 'resourceGroup'

type ManagedIdentities = {
  clusterApiAzureMiName: string
  controlPlaneMiName: string
  cloudControllerManagerMiName: string
  ingressMiName: string
  diskCsiDriverMiName: string
  fileCsiDriverMiName: string
  imageRegistryMiName: string
  cloudNetworkConfigMiName: string
  kmsMiName: string
  dpDiskCsiDriverMiName: string
  dpFileCsiDriverMiName: string
  dpImageRegistryMiName: string
  serviceManagedIdentityName: string
}
@description('Identities to assign')
param identities ManagedIdentities

@description('Resource group name where identities are located')
param resourceGroupName string

@description('The Network security group name for the HCP cluster resources')
param nsgName string

@description('The virtual network name for the HCP cluster resources')
param vnetName string

@description('The subnet name for deploying HCP cluster resources')
param subnetName string

@description('The KeyVault name that contains the etcd encryption key')
param keyVaultName string

//
// E X I S T I N G   R E S O U R C E S
//

resource vnet 'Microsoft.Network/virtualNetworks@2022-07-01' existing = {
  name: vnetName
}

resource subnet 'Microsoft.Network/virtualNetworks/subnets@2022-07-01' existing = {
  name: subnetName
  parent: vnet
}

resource nsg 'Microsoft.Network/networkSecurityGroups@2022-07-01' existing = {
  name: nsgName
}

resource keyVault 'Microsoft.KeyVault/vaults@2024-12-01-preview' existing = {
  name: keyVaultName
}


//
// C O N T R O L   P L A N E   I D E N T I T I E S
//

//
// C L U S T E R   A P I   A Z U R E   M I
//

resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.clusterApiAzureMiName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift Hosted Control Planes Cluster API Provider: Enables permissions to allow cluster API to 
// manage nodes, networks and disks for OpenShift cluster.
// https://www.azadvertizer.net/azrolesadvertizer/88366f10-ed47-4cc0-9fab-c8a06148393e.html
var hcpClusterApiProviderRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '88366f10-ed47-4cc0-9fab-c8a06148393e'
)

resource hcpClusterApiProviderRoleSubnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, clusterApiAzureMi.id, hcpClusterApiProviderRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: clusterApiAzureMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: hcpClusterApiProviderRoleId
  }
}


//
// C O N T R O L   P L A N E   O P E R A T O R   M A N A G E D   I D E N T I T Y
//

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.controlPlaneMiName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift Hosted Control Planes Control Plane Operator: Enables the control plane operator to read 
// resources necessary for OpenShift cluster.
// https://www.azadvertizer.net/azrolesadvertizer/fc0c873f-45e9-4d0d-a7d1-585aab30c6ed.html
var hcpControlPlaneOperatorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'fc0c873f-45e9-4d0d-a7d1-585aab30c6ed'
)

resource hcpControlPlaneOperatorVnetRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, controlPlaneMi.id, hcpControlPlaneOperatorRoleId, vnet.id)
  scope: vnet
  properties: {
    principalId: controlPlaneMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: hcpControlPlaneOperatorRoleId
  }
}

resource hcpControlPlaneOperatorNsgRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, controlPlaneMi.id, hcpControlPlaneOperatorRoleId, nsg.id)
  scope: nsg
  properties: {
    principalId: controlPlaneMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: hcpControlPlaneOperatorRoleId
  }
}


//
// C L O U D   C O N T R O L L E R   M A N A G E R   M A N A G E D   I D E N T I T Y
//

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.cloudControllerManagerMiName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift Cloud Controller Manager: Manage and update the cloud controller manager deployed on top of OpenShift.
// https://www.azadvertizer.net/azrolesadvertizer/a1f96423-95ce-4224-ab27-4e3dc72facd4.html
var cloudControllerManagerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'a1f96423-95ce-4224-ab27-4e3dc72facd4'
)

resource cloudControllerManagerRoleSubnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, cloudControllerManagerMi.id, cloudControllerManagerRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: cloudControllerManagerMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: cloudControllerManagerRoleId
  }
}

resource cloudControllerManagerRoleNsgAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, cloudControllerManagerMi.id, cloudControllerManagerRoleId, nsg.id)
  scope: nsg
  properties: {
    principalId: cloudControllerManagerMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: cloudControllerManagerRoleId
  }
}


//
// I N G R E S S   M A N A G E D   I D E N T I T Y
//

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.ingressMiName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift Cluster Ingress Operator: Manage and configure the OpenShift router.
// https://www.azadvertizer.net/azrolesadvertizer/0336e1d3-7a87-462b-b6db-342b63f7802c.html
var ingressOperatorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '0336e1d3-7a87-462b-b6db-342b63f7802c'
)

resource ingressOperatorRoleSubnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, ingressMi.id, ingressOperatorRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: ingressMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: ingressOperatorRoleId
  }
}


//
// D I S K   C S I   D R I V E R   M A N A G E D   I D E N T I T Y
//

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.diskCsiDriverMiName
  scope: resourceGroup(resourceGroupName)
}


//
// F I L E   C S I   D R I V E R   M A N A G E D   I D E N T I T Y
//

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.fileCsiDriverMiName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift File Storage Operator: Install Container Storage Interface (CSI) drivers that enable your
// cluster to use Azure Files. Set OpenShift cluster-wide storage defaults to ensure a default storageclass exists for clusters.
// https://www.azadvertizer.net/azrolesadvertizer/0d7aedc0-15fd-4a67-a412-efad370c947e.html
var fileStorageOperatorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '0d7aedc0-15fd-4a67-a412-efad370c947e'
)

resource fileStorageOperatorRoleSubnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, fileCsiDriverMi.id, fileStorageOperatorRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: fileCsiDriverMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: fileStorageOperatorRoleId
  }
}

resource fileStorageOperatorRoleNsgAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, fileCsiDriverMi.id, fileStorageOperatorRoleId, nsg.id)
  scope: nsg
  properties: {
    principalId: fileCsiDriverMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: fileStorageOperatorRoleId
  }
}


//
// I M A G E   R E G I S T R Y   M A N A G E D   I D E N T I T Y
//

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.imageRegistryMiName
  scope: resourceGroup(resourceGroupName)
}


//
// C L O U D   N E T W O R K   C O N F I G   M A N A G E D   I D E N T I T Y
//

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.cloudNetworkConfigMiName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift Network Operator: Install and upgrade the networking components on an OpenShift cluster.
// https://www.azadvertizer.net/azrolesadvertizer/be7a6435-15ae-4171-8f30-4a343eff9e8f.html
var networkOperatorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'be7a6435-15ae-4171-8f30-4a343eff9e8f'
)

resource networkOperatorRoleSubnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, cloudNetworkConfigMi.id, networkOperatorRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: cloudNetworkConfigMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: networkOperatorRoleId
  }
}

resource networkOperatorRoleVnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, cloudNetworkConfigMi.id, networkOperatorRoleId, vnet.id)
  scope: vnet
  properties: {
    principalId: cloudNetworkConfigMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: networkOperatorRoleId
  }
}


//
// D A T A P L A N E   I D E N T I T I E S
//

resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.dpDiskCsiDriverMiName
  scope: resourceGroup(resourceGroupName)
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.dpFileCsiDriverMiName
  scope: resourceGroup(resourceGroupName)
}

resource dpFileCsiDriverFileStorageOperatorRoleSubnetAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, dpFileCsiDriverMi.id, fileStorageOperatorRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: dpFileCsiDriverMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: fileStorageOperatorRoleId
  }
}

resource dpFileCsiDriverFileStorageOperatorRoleNsgAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, dpFileCsiDriverMi.id, fileStorageOperatorRoleId, nsg.id)
  scope: nsg
  properties: {
    principalId: dpFileCsiDriverMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: fileStorageOperatorRoleId
  }
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.dpImageRegistryMiName
  scope: resourceGroup(resourceGroupName)
}

//
// S E R V I C E   M A N A G E D   I D E N T I T Y
//

resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.serviceManagedIdentityName
  scope: resourceGroup(resourceGroupName)
}

// Azure Red Hat OpenShift Hosted Control Planes Service Managed Identity
// https://www.azadvertizer.net/azrolesadvertizer/c0ff367d-66d8-445e-917c-583feb0ef0d4.html
var hcpServiceManagedIdentityRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'c0ff367d-66d8-445e-917c-583feb0ef0d4'
)

// grant service managed identity role to the service managed identity over the user provided subnet
resource serviceManagedIdentityRoleAssignmentVnet 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, hcpServiceManagedIdentityRoleId, vnet.id)
  scope: vnet
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: hcpServiceManagedIdentityRoleId
  }
}

// grant service managed identity role to the service managed identity over the user provided subnet
resource serviceManagedIdentityRoleAssignmentSubnet 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, hcpServiceManagedIdentityRoleId, subnet.id)
  scope: subnet
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: hcpServiceManagedIdentityRoleId
  }
}

// grant service managed identity role to the service managed identity over the user provided NSG
resource serviceManagedIdentityRoleAssignmentNSG 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, hcpServiceManagedIdentityRoleId, nsg.id)
  scope: nsg
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: hcpServiceManagedIdentityRoleId
  }
}

//
// KMS identity
//

resource kmsMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.kmsMiName
  scope: resourceGroup(resourceGroupName)
}

// Key Vault Crypto User: Perform cryptographic operations using keys. Only works for key vaults that use the
//'Azure role-based access control' permission model.
// https://www.azadvertizer.net/azrolesadvertizer/12338af0-0e69-4776-bea7-57ae8d297424.html
var keyVaultCryptoUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '12338af0-0e69-4776-bea7-57ae8d297424'
)

resource keyVaultCryptoUserToKeyVaultRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, kmsMi.id, keyVaultCryptoUserRoleId, keyVault.id)
  scope: keyVault
  properties: {
    principalId: kmsMi.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: keyVaultCryptoUserRoleId
  }
}

//
// Outputs
//

output userAssignedIdentitiesValue object = {
  controlPlaneOperators: {
    'cluster-api-azure': clusterApiAzureMi.id
    'control-plane': controlPlaneMi.id
    'cloud-controller-manager': cloudControllerManagerMi.id
    'ingress': ingressMi.id
    'disk-csi-driver': diskCsiDriverMi.id
    'file-csi-driver': fileCsiDriverMi.id
    'image-registry': imageRegistryMi.id
    'cloud-network-config': cloudNetworkConfigMi.id
    'kms': kmsMi.id
  }
  dataPlaneOperators: {
    'disk-csi-driver': dpDiskCsiDriverMi.id
    'file-csi-driver': dpFileCsiDriverMi.id
    'image-registry': dpImageRegistryMi.id
  }
  serviceManagedIdentity: serviceManagedIdentity.id
}

output identityValue object = {
  type: 'UserAssigned'
  userAssignedIdentities: {
    '${serviceManagedIdentity.id}': {}
    '${clusterApiAzureMi.id}': {}
    '${controlPlaneMi.id}': {}
    '${cloudControllerManagerMi.id}': {}
    '${ingressMi.id}': {}
    '${diskCsiDriverMi.id}': {}
    '${fileCsiDriverMi.id}': {}
    '${imageRegistryMi.id}': {}
    '${cloudNetworkConfigMi.id}': {}
    '${kmsMi.id}': {}
  }
}
