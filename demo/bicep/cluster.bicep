@description('Name of the hypershift cluster')
param clusterName string

@description('Managed resource group name')
param managedResourceGroupName string

@description('Location for the hypershift cluster')
param location string

@description('Name of the customer nsg')
param customerNsgName string

@description('Name of the customer vnet')
param customerVnetName string

@description('Name of the customer vnet subnet')
param customerVnetSubnetName string

var randomSuffix = toLower(uniqueString(clusterName))

module customerInfra 'customer-infra.bicep' = {
  name: 'customer-infra'
  params: {
    customerNsgName: customerNsgName
    customerVnetName: customerVnetName
    customerVnetSubnet: customerVnetSubnetName
  }
}

// Control plane identities
resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cluster-api-azure-${randomSuffix}'
  location: location
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-control-plane-${randomSuffix}'
  location: location
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-controller-manager-${randomSuffix}'
  location: location
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-ingress-${randomSuffix}'
  location: location
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-disk-csi-driver-${randomSuffix}'
  location: location
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-file-csi-driver-${randomSuffix}'
  location: location
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-image-registry-${randomSuffix}'
  location: location
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-network-config-${randomSuffix}'
  location: location
}

// Data plane identities
resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-disk-csi-driver-${randomSuffix}'
  location: location
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-file-csi-driver-${randomSuffix}'
  location: location
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-image-registry-${randomSuffix}'
  location: location
}

// Service managed identity
resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-service-managed-identity-${randomSuffix}'
  location: location
}

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' = {
  name: clusterName
  location: location
  properties: {
    version: {
      id: 'openshift-v4.18.1'
      channelGroup: 'stable'
    }
    dns: {}
    network: {
      networkType: 'OVNKubernetes'
      podCidr: '10.128.0.0/14'
      serviceCidr: '172.30.0.0/16'
      machineCidr: '10.0.0.0/16'
      hostPrefix: 23
    }
    console: {}
    api: {
      visibility: 'public'
    }
    platform: {
      managedResourceGroup: managedResourceGroupName
      subnetId: customerInfra.outputs.subnetId
      outboundType: 'loadBalancer'
      networkSecurityGroupId: customerInfra.outputs.networkSecurityGroupId
      operatorsAuthentication: {
        userAssignedIdentities: {
          controlPlaneOperators: {
            'cluster-api-azure': clusterApiAzureMi.id
            'control-plane': controlPlaneMi.id
            'cloud-controller-manager': cloudControllerManagerMi.id
            'ingress': ingressMi.id
            'disk-csi-driver': diskCsiDriverMi.id
            'file-csi-driver': fileCsiDriverMi.id
            'image-registry': imageRegistryMi.id
            'cloud-network-config': cloudNetworkConfigMi.id
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
    }
  }
}
