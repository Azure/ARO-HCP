@description('The name of the AKS Managed Cluster resource.')
param aksClusterName string = 'aro-hcp-cluster-001'

// TODO: When the work around workload identity for the RP is finalized,
// change this to true
@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool = false

@description('Optional DNS prefix to use with hosted Kubernetes API server FQDN.')
param dnsPrefix string = aksClusterName

@description('(Optional) boolean flag to configure public/private AKS Cluster')
param enablePrivateCluster bool = true

@description('The min number of agent nodes.')
param agentMinCount int = 2

@description('The max number of agent nodes.')
param agentMaxCount int = 3

@description('The size of the Virtual Machine.')
param agentVMSize string = 'Standard_D2s_v3'

@description('Disk size (in GB) to provision for each of the agent pool nodes. This value ranges from 0 to 1023. Specifying 0 will apply the default disk size for that agentVMSize.')
@minValue(0)
@maxValue(1023)
param osDiskSizeGB int = 32

@description('The version of Kubernetes.')
param kubernetesVersion string

@description('VNET address prefix')
param vnetAddressPrefix string = '10.128.0.0/14'

@description('Subnet address prefix')
param subnetPrefix string = '10.128.8.0/21'

@description('Specifies the address prefix of the subnet hosting the pods of the AKS cluster.')
param podSubnetPrefix string = '10.128.64.0/18'

@description('A CIDR notation IP range from which to assign service cluster IPs.')
param serviceCidr string = '10.130.0.0/16'

@description('Containers DNS server IP address.')
param dnsServiceIP string = '10.130.0.10'

@description('Current user id')
param currentUserId string

param location string = resourceGroup().location
param createdByConfigTag string = 'None'

var aksClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '0ab0b1a8-8aac-4efd-b8c2-3ee1fb270be8'
)
var aksClusterRbacClusterAdminRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)
var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)

resource frontend_mi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  location: location
  name: 'frontend-${location}'
}

resource frontend_mi_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = {
  name: 'frontend-${location}-fedcred'
  parent: frontend_mi
  properties: {
    audiences: [
      'api://AzureADTokenExchange'
    ]
    issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
    subject: 'system:serviceaccount:aro-hcp:frontend'
  }
}

resource aks_nsg 'Microsoft.Network/networkSecurityGroups@2023-09-01' = {
  name: 'aks-nsg'
  location: location
}

resource aks_pod_nsg 'Microsoft.Network/networkSecurityGroups@2023-09-01' = {
  name: 'aks-pod-nsg'
  location: location
}

resource aks_keyvault 'Microsoft.KeyVault/vaults@2023-07-01' = {
  location: location
  name: take('aks-kv-${uniqueString(currentUserId)}', 24)
  tags: {
    resourceGroup: resourceGroup().name 
  }
  properties: {
    enableRbacAuthorization: true
    enabledForDeployment: false
    enabledForDiskEncryption: false
    enabledForTemplateDeployment: false
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Deny'
      ipRules: [
        {
          // TODO: restrict in higher environments
          value: '0.0.0.0/0'
        }
      ]
    }
    // TODO: disabled in higher environments
    publicNetworkAccess: 'Enabled'
    sku: {
      name: 'standard'
      family: 'A'
    }
    tenantId: subscription().tenantId
  }
}

resource aks_etcd_kms 'Microsoft.KeyVault/vaults/keys@2023-07-01' = {
  parent: aks_keyvault
  name: 'aks-etcd-encryption'
  properties: {
    kty: 'RSA'
    keyOps: [
      'encrypt'
      'decrypt'
    ]
    keySize: 2048
  }
}

@description('Perform cryptographic operations using keys. Only works for key vaults that use the Azure role-based access control permission model.')
var keyVaultCryptoUserId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '12338af0-0e69-4776-bea7-57ae8d297424'
)

resource aks_keyvault_crypto_user 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksClusterUserDefinedManagedIdentity.id, keyVaultCryptoUserId, aks_keyvault.id)
  scope: aks_keyvault
  properties: {
    roleDefinitionId: keyVaultCryptoUserId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

resource vnet 'Microsoft.Network/virtualNetworks@2023-09-01' = {
  location: location
  name: 'aks-net'
  tags: {
    sharedhcp: 'true'
  }
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
  }
}

resource aksNodeSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-09-01' = {
  parent: vnet
  name: 'ClusterSubnet-001'
  properties: {
    addressPrefix: subnetPrefix
    networkSecurityGroup: {
      id: aks_nsg.id
    }
    privateEndpointNetworkPolicies: 'Disabled'
    serviceEndpoints: [
      {
        service: 'Microsoft.AzureCosmosDB'
      }
      {
        service: 'Microsoft.ContainerRegistry'
      }
      {
        service: 'Microsoft.Storage'
      }
      {
        service: 'Microsoft.KeyVault'
      }
    ]
  }
}

resource aksPodSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-09-01' = {
  parent: vnet
  name: 'PodSubnet-001'
  properties: {
    addressPrefix: podSubnetPrefix
    networkSecurityGroup: {
      id: aks_pod_nsg.id
    }
    privateEndpointNetworkPolicies: 'Disabled'
    serviceEndpoints: [
      {
        service: 'Microsoft.Storage'
      }
    ]
    delegations: [
      {
        name: 'AKS'
        properties: {
          serviceName: 'Microsoft.ContainerService/managedClusters'
        }
      }
    ]
  }
}

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${aksClusterName}-msi'
  location: location
}

resource aksNetworkContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: vnet
  name: guid(aksClusterUserDefinedManagedIdentity.id, networkContributorRoleId, aksNodeSubnet.id)
  properties: {
    roleDefinitionId: networkContributorRoleId
    principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-01-01' = {
  location: location
  name: aksClusterName
  tags: {
    CreatedByConfig: createdByConfigTag
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${aksClusterUserDefinedManagedIdentity.id}': {}
    }
  }
  properties: {
    aadProfile: {
      managed: true
      enableAzureRBAC: true
    }
    nodeResourceGroup: '${resourceGroup().name}-aks1'
    apiServerAccessProfile: {
      enablePrivateCluster: enablePrivateCluster
    }
    addonProfiles: {
      azureKeyvaultSecretsProvider: {
        enabled: true
        config: {
          enableSecretRotation: 'true'
          rotationPollInterval: '24h'
          syncSecret: 'true'
        }
      }
    }
    kubernetesVersion: kubernetesVersion
    enableRBAC: true
    dnsPrefix: dnsPrefix
    agentPoolProfiles: [
      {
        name: 'system'
        osType: 'Linux'
        osSKU: 'AzureLinux'
        mode: 'System'
        orchestratorVersion: kubernetesVersion
        enableAutoScaling: true
        enableEncryptionAtHost: true
        enableFIPS: true
        osDiskType: 'Ephemeral'
        osDiskSizeGB: osDiskSizeGB
        count: agentMinCount
        minCount: agentMinCount
        maxCount: agentMaxCount
        vmSize: agentVMSize
        vnetSubnetID: aksNodeSubnet.id
        podSubnetID: aksPodSubnet.id
        maxPods: 250
      }
    ]
    networkProfile: {
      networkDataplane: 'cilium'
      networkPolicy: 'cilium'
      networkPlugin: 'azure'
      serviceCidr: serviceCidr
      dnsServiceIP: dnsServiceIP
    }
    autoScalerProfile: {
      'balance-similar-node-groups': 'false'
      'scan-interval': '10s'
      'scale-down-delay-after-add': '10m'
      'scale-down-delay-after-delete': '20s'
      'scale-down-delay-after-failure': '3m'
      'scale-down-unneeded-time': '10m'
      'scale-down-unready-time': '20m'
      'scale-down-utilization-threshold': '0.5'
      'skip-nodes-with-local-storage': 'false'
      'max-graceful-termination-sec': '600'
      'max-node-provision-time': '15m'
    }
    autoUpgradeProfile: {
      nodeOSUpgradeChannel: 'NodeImage'
      upgradeChannel: 'node-image'
    }
    oidcIssuerProfile: {
      enabled: true
    }
    securityProfile: {
      azureKeyVaultKms: {
        enabled: true
        keyId: aks_etcd_kms.properties.keyUriWithVersion
        keyVaultNetworkAccess: 'Public'
      }
      imageCleaner: {
        enabled: true
        intervalHours: 24
      }
      workloadIdentity: {
        enabled: true
      }
    }
    serviceMeshProfile: {
      mode: 'Istio'
      istio: {
        components: {
          ingressGateways: [
            {
              enabled: true
              mode: 'External'
            }
          ]
        }
      }
    }
  }
}

// az aks command invoke --resource-group hcp-standalone-mshen --name aro-hcp-cluster-001 --command "kubectl get ns"
resource currentUserAksClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' =
  if (length(currentUserId) > 0) {
    scope: aksCluster
    name: guid(location, aksClusterName, aksClusterAdminRoleId, currentUserId)
    properties: {
      roleDefinitionId: aksClusterAdminRoleId
      principalId: currentUserId
      principalType: 'User'
    }
  }

// az aks command invoke --resource-group hcp-standalone-mshen --name aro-hcp-cluster-001 --command "kubectl get ns"
resource currentUserAksRbacClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' =
  if (length(currentUserId) > 0) {
    scope: aksCluster
    name: guid(location, aksClusterName, aksClusterRbacClusterAdminRoleId, currentUserId)
    properties: {
      roleDefinitionId: aksClusterRbacClusterAdminRoleId
      principalId: currentUserId
      principalType: 'User'
    }
  }

@description('Deploy ARO HCP RP Azure Cosmos DB if true')
param deployFrontendCosmos bool = true

module nestedPeeringTemplate './rp-cosmos.bicep' =
  if (deployFrontendCosmos) {
    name: 'nestedTemplate1'
    scope: resourceGroup()
    params: {
      location: location
      aksNodeSubnetId: aksNodeSubnet.id
      vnetId: vnet.id
      disableLocalAuth: disableLocalAuth
      userAssignedMI: frontend_mi.id
      uamiPrincipalId: frontend_mi.properties.principalId
    }
  }

output frontend_mi_client_id string = frontend_mi.properties.clientId
