@description('Name of the hypershift cluster')
param clusterName string

@description('The Hypershift cluster managed resource group name')
param managedResourceGroupName string

@description('The Network security group name for the hcp cluster resources')
param nsgName string

@description('The virtual network name for the hcp cluster resources')
param vnetName string

@description('The subnet name for deploying hcp cluster resources.')
param subnetName string

@description('The KeyVault name that contains the encryption key')
param keyVaultName string

var etcdEncryptionKeyName = 'etcd-data-kms-encryption-key'
var randomSuffix = toLower(uniqueString(clusterName))

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

resource etcdEncryptionKey 'Microsoft.KeyVault/vaults/keys@2024-12-01-preview' existing = {
  parent: keyVault
  name: etcdEncryptionKeyName
}

//
// C O N T R O L   P L A N E   I D E N T I T I E S
//

// Reader
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

//
// C L U S T E R   A P I   A Z U R E   M I
//

resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cluster-api-azure-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift Hosted Control Planes Cluster API Provider
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

resource serviceManagedIdentityReaderOnClusterApiAzureMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, clusterApiAzureMi.id)
  scope: clusterApiAzureMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}


//
// K M S   M I
//

resource kmsMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-kms-${randomSuffix}'
  location: resourceGroup().location
}

// Key Vault Crypto User
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

resource serviceManagedIdentityReaderOnKmsMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, kmsMi.id)
  scope: kmsMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// C O N T R O L   P L A N E   O P E R A T O R   M A N A G E D   I D E N T I T Y
//

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-control-plane-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift Hosted Control Planes Control Plane Operator
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

resource serviceManagedIdentityReaderOnControlPlaneMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, controlPlaneMi.id)
  scope: controlPlaneMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// C L O U D   C O N T R O L L E R   M A N A G E R   M A N A G E D   I D E N T I T Y
//

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-controller-manager-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift Cloud Controller Manager
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

resource serviceManagedIdentityReaderOnCloudControllerManagerMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, cloudControllerManagerMi.id)
  scope: cloudControllerManagerMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// I N G R E S S   M A N A G E D   I D E N T I T Y
//

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-ingress-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift Cluster Ingress Operator
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

resource serviceManagedIdentityReaderOnIngressMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, ingressMi.id)
  scope: ingressMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// D I S K   C S I   D R I V E R   M A N A G E D   I D E N T I T Y
//

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-disk-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource serviceManagedIdentityReaderOnDiskCsiDriverMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, diskCsiDriverMi.id)
  scope: diskCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// F I L E   C S I   D R I V E R   M A N A G E D   I D E N T I T Y
//

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-file-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift File Storage Operator
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

resource serviceManagedIdentityReaderOnFileCsiDriverMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, fileCsiDriverMi.id)
  scope: fileCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// I M A G E   R E G I S T R Y   M A N A G E D   I D E N T I T Y
//

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-image-registry-${randomSuffix}'
  location: resourceGroup().location
}

resource serviceManagedIdentityReaderOnImageRegistryMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, imageRegistryMi.id)
  scope: imageRegistryMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// C L O U D   N E T W O R K   C O N F I G   M A N A G E D   I D E N T I T Y
//

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-network-config-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift Network Operator
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

resource serviceManagedIdentityReaderOnCloudNetworkMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, cloudNetworkConfigMi.id)
  scope: cloudNetworkConfigMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// D A T A P L A N E   I D E N T I T I E S
//

// Azure Red Hat OpenShift Federated Credential
// give the ability to perform OIDC federation to the service managed identity
// over the corresponding dataplane identities
var federatedCredentialsRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'ef318e2a-8334-4a05-9e4a-295a196c6a6e'
)

resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-disk-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource dpDiskCsiDriverMiFederatedCredentialsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, dpDiskCsiDriverMi.id, federatedCredentialsRoleId)
  scope: dpDiskCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-file-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource dpFileCsiDriverMiFederatedCredentialsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, dpFileCsiDriverMi.id, federatedCredentialsRoleId)
  scope: dpFileCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
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

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-image-registry-${randomSuffix}'
  location: resourceGroup().location
}

resource dpImageRegistryMiFederatedCredentialsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, dpImageRegistryMi.id, federatedCredentialsRoleId)
  scope: dpImageRegistryMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
}

//
// S E R V I C E   M A N A G E D   I D E N T I T Y
//

resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-service-managed-identity-${randomSuffix}'
  location: resourceGroup().location
}

// Azure Red Hat OpenShift Hosted Control Planes Service Managed Identity
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

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' = {
  name: clusterName
  location: resourceGroup().location
  properties: {
    dns: {}
    network: {
      networkType: 'OVNKubernetes'
      podCidr: '10.128.0.0/14'
      serviceCidr: '172.30.0.0/16'
      machineCidr: '10.0.0.0/16'
      hostPrefix: 23
    }
    console: {}
    etcd: {
      dataEncryption: {
        keyManagementMode: 'CustomerManaged'
        customerManaged: {
          encryptionType: 'KMS'
          kms: {
             activeKey: {
              vaultName: keyVaultName
              name: etcdEncryptionKeyName
              version: last(split(etcdEncryptionKey.properties.keyUriWithVersion, '/'))
             }
          }
        }
      }
    }
    api: {
      visibility: 'Public'
    }
    clusterImageRegistry: {
      state: 'Enabled'
    }
    platform: {
      managedResourceGroup: managedResourceGroupName
      subnetId: subnet.id
      outboundType: 'LoadBalancer'
      networkSecurityGroupId: nsg.id
      operatorsAuthentication: {
        userAssignedIdentities: {
          controlPlaneOperators: {
            'cluster-api-azure': clusterApiAzureMi.id
            'control-plane': controlPlaneMi.id
            'cloud-controller-manager': cloudControllerManagerMi.id
            #disable-next-line prefer-unquoted-property-names
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
      }
    }
  }
  identity: {
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
  dependsOn: [
    hcpClusterApiProviderRoleSubnetAssignment
    keyVaultCryptoUserToKeyVaultRoleAssignment
    hcpControlPlaneOperatorVnetRoleAssignment
    hcpControlPlaneOperatorNsgRoleAssignment
    cloudControllerManagerRoleSubnetAssignment
    cloudControllerManagerRoleNsgAssignment
    ingressOperatorRoleSubnetAssignment
    fileStorageOperatorRoleSubnetAssignment
    fileStorageOperatorRoleNsgAssignment
    networkOperatorRoleSubnetAssignment
    networkOperatorRoleVnetAssignment
    dpDiskCsiDriverMiFederatedCredentialsRoleAssignment
    dpFileCsiDriverMiFederatedCredentialsRoleAssignment
    dpImageRegistryMiFederatedCredentialsRoleAssignment
    serviceManagedIdentityRoleAssignmentVnet
    serviceManagedIdentityRoleAssignmentSubnet
    serviceManagedIdentityRoleAssignmentNSG
    dpFileCsiDriverFileStorageOperatorRoleSubnetAssignment
    dpFileCsiDriverFileStorageOperatorRoleNsgAssignment
    serviceManagedIdentityReaderOnControlPlaneMi
    serviceManagedIdentityReaderOnCloudControllerManagerMi
    serviceManagedIdentityReaderOnIngressMi
    serviceManagedIdentityReaderOnDiskCsiDriverMi
    serviceManagedIdentityReaderOnFileCsiDriverMi
    serviceManagedIdentityReaderOnImageRegistryMi
    serviceManagedIdentityReaderOnCloudNetworkMi
    serviceManagedIdentityReaderOnClusterApiAzureMi
    serviceManagedIdentityReaderOnKmsMi
  ]
}
