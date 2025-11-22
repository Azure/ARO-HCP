@description('Name of the hypershift cluster')
param clusterName string

@description('The Hypershift cluster managed resource group name')
param managedResourceGroupName string = '${resourceGroup().name}-managed'

@description('The Network security group name for the hcp cluster resources')
param nsgName string

@description('The virtual network name for the hcp cluster resources')
param vnetName string

@description('The subnet name for deploying hcp cluster resources.')
param subnetName string

@description('OpenShift Version ID to use')
param openshiftVersionId string = '4.19'

@description('Network configuration of the hosted cluster')
param networkConfig object = {
  networkType: 'OVNKubernetes'
  podCidr: '10.128.0.0/14'
  serviceCidr: '172.30.0.0/16'
  machineCidr: '10.0.0.0/16'
  hostPrefix: 23
}

@description('Cluster Managed Identities: ')
param userAssignedIdentitiesValue object

@description('Cluster Managed Identities')
param identityValue object

@description('The KeyVault name that contains the etcd encryption key')
param keyVaultName string

@description('The name of the etcd encryption key in the KeyVault')
param etcdEncryptionKeyName string

@description('List of authorized IP ranges for API server access')
param authorizedCidrs array = []

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
    network: networkConfig
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
      authorizedCidrs: authorizedCidrs
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
