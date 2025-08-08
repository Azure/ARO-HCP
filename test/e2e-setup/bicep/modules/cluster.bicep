@description('Name of the hypershift cluster')
param clusterName string

@description('The Hypershift cluster managed resource group name')
param managedResourceGroupName string = '${clusterName}-rg'

@description('The Network security group name for the hcp cluster resources')
param nsgName string

@description('The virtual network name for the hcp cluster resources')
param vnetName string

@description('The subnet name for deploying hcp cluster resources.')
param subnetName string

@description('OpenShift Version ID to use')
param openshiftVersionId string = '4.19'

@description('Cluster Managed Identities: ')
param userAssignedIdentitiesValue object

@description('Cluster Managed Identities')
param identityValue object

var randomSuffix = toLower(uniqueString(resourceGroup().id))

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

//
// Hosted cluster
//

resource hcp 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters@2024-06-10-preview' = {
  name: clusterName
  location: resourceGroup().location
  properties: {
    version: {
      id: openshiftVersionId
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
    etcd: {
      dataEncryption: {
        keyManagementMode: 'PlatformManaged'
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
        userAssignedIdentities: userAssignedIdentitiesValue
      }
    }
  }
  identity: identityValue
}

output name string = clusterName
