// Everything that needs the management cluster's AKS ManagedCluster resource to
// already exist: ACR pull roles, workload-identity federated credentials, the
// image-puller identity, and the aroDevopsMSI cluster-admin role assignment.
// This is the post-ManagedCluster half of aks-cluster-base.bicep (the module
// svc-cluster.bicep still uses as-is); management clusters call this module
// instead, from mgmt-cluster.bicep, since their ManagedCluster + node pools are
// created by the aks-cluster-create Go tool rather than a bicep resource in this
// same deployment.
import * as res from 'resource.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('MSI that will take actions on the AKS cluster during service deployment time')
param deploymentMsiId string

@description('The resource IDs of ACR instances that the AKS cluster will pull images from')
param pullAcrResourceIds array = []

@description('Workload identities (namespace/serviceAccountName) that need a federated credential on the cluster OIDC issuer')
param workloadIdentities array

resource aksCluster 'Microsoft.ContainerService/managedClusters@2026-04-02-preview' existing = {
  name: aksClusterName
}

//
// ACR Pull Permissions on the own resource group and the resource groups provided
// by acrResourceGroups
//

var acrPullRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

var acrReferences = [for acrId in pullAcrResourceIds: res.acrRefFromId(acrId)]

module acrPullRole 'acr/acr-permissions.bicep' = [
  for acrRef in acrReferences: {
    name: guid(acrRef.name, aksCluster.id, acrPullRoleDefinitionId)
    scope: resourceGroup(acrRef.resourceGroup.subscriptionId, acrRef.resourceGroup.name)
    params: {
      principalIds: [aksCluster.properties.identityProfile.kubeletidentity.objectId]
      acrName: acrRef.name
      grantPullAccess: true
    }
  }
]

//
//   W O R K L O A D   I D E N T I T I E S
//

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = [
  for wi in workloadIdentities: {
    name: wi.value.uamiName
  }
]

resource uami_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(workloadIdentities)): {
    parent: uami[i]
    name: '${workloadIdentities[i].value.uamiName}-${location}-fedcred'
    properties: {
      audiences: [
        'api://AzureADTokenExchange'
      ]
      issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
      subject: 'system:serviceaccount:${workloadIdentities[i].value.namespace}:${workloadIdentities[i].value.serviceAccountName}'
    }
  }
]

//
//  A C R   P U L L   C O N T R O L L E R
//

resource pullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  location: location
  name: 'image-puller'
}

module acrPullerRoles 'acr/acr-permissions.bicep' = [
  for acrRef in acrReferences: {
    name: guid(acrRef.name, aksCluster.id, acrPullRoleDefinitionId, 'puller-identity')
    scope: resourceGroup(acrRef.resourceGroup.subscriptionId, acrRef.resourceGroup.name)
    params: {
      principalIds: [pullerIdentity.properties.principalId]
      acrName: acrRef.name
      grantPullAccess: true
    }
  }
]

@batchSize(1)
resource puller_fedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(workloadIdentities)): {
    parent: pullerIdentity
    name: '${workloadIdentities[i].value.uamiName}-${location}-puller-fedcred'
    properties: {
      audiences: [
        'api://AzureCRTokenExchange'
      ]
      issuer: aksCluster.properties.oidcIssuerProfile.issuerURL
      subject: 'system:serviceaccount:${workloadIdentities[i].value.namespace}:${workloadIdentities[i].value.serviceAccountName}'
    }
  }
]

// grant aroDevopsMsi the aksClusterAdmin role on the aksCluster so it can
// deploy services to the cluster
//
// Azure Kubernetes Service RBAC Cluster Admin Role
// https://www.azadvertizer.net/azrolesadvertizer/b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b.html
var aksClusterAdminRBACRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b'
)

resource aroDevopsMSIClusterAdmin 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(aksCluster.id, deploymentMsiId, aksClusterAdminRBACRoleId)
  scope: aksCluster
  properties: {
    principalId: reference(deploymentMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: aksClusterAdminRBACRoleId
  }
}
