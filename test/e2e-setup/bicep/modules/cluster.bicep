@description('Name of the hypershift cluster')
param clusterName string

@description('The Hypershift cluster managed resource group name')
param managedResourceGroupName string = '${clusterName}-rg'

@description('The Network security group ID for the hcp cluster resources')
param networkSecurityGroupId string

@description('The subnet id for deploying hcp cluster resources.')
param subnetId string

@description('OpenShift Version ID to use')
param openshiftVersionId string = 'openshift-v4.19.0'

@description('Cluster Managed Identities: ')
param userAssignedIdentitiesValue object

@description('Cluster Managed Identities')
param identityValue object

var randomSuffix = toLower(uniqueString(resourceGroup().id))

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
    platform: {
      managedResourceGroup: managedResourceGroupName
      subnetId: subnetId
      outboundType: 'LoadBalancer'
      networkSecurityGroupId: networkSecurityGroupId
      operatorsAuthentication: {
        userAssignedIdentities: userAssignedIdentitiesValue
      }
    }
  }
  identity: identityValue
}
