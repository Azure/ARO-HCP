import * as res from '../modules/resource.bicep'

@description('Azure Region Location')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string = 'aro-hcp-aks'

@description('Resource ID of the node subnet, from mgmt-infra.bicep')
param nodeSubnetId string

@description('Resource ID of the VNet, from mgmt-infra.bicep')
param vnetId string

@description('The resource ID of the OCP ACR')
param ocpAcrResourceId string

@description('The resource ID of the SVC ACR')
param svcAcrResourceId string

@description('The name of the maestro consumer.')
param maestroConsumerName string

@description('The SAN and CN for the Maestro consumer EventGrid certificate.')
param maestroConsumerCertSAN string

@description('The issuer of the Maestro certificate.')
param maestroCertIssuer string

@description('The Azure resource ID of the eventgrid namespace for Maestro.')
param maestroEventGridNamespaceId string

@description('The name of the maestro consumer.')
param maestroConsumerMIName string

@description('The namespace of the maestro consumer.')
param maestroConsumerNamespace string

@description('The service account name of the maestro consumer.')
param maestroConsumerServiceAccountName string

@description('The resource ID of the Cosmos DB account for the RP')
param rpCosmosDbAccountId string

@description('If true, make the Cosmos DB instance private')
param rpCosmosDbPrivate bool

@description('The name of the kube-applier managed identity.')
param kubeApplierMIName string

@description('The namespace for kube-applier.')
param kubeApplierNamespace string

@description('The service account name for kube-applier.')
param kubeApplierServiceAccountName string

@description('The CosmosDB container name for kube-applier.')
param kubeApplierContainerName string

@description('The autoscale max throughput for the kube-applier CosmosDB container.')
param kubeApplierContainerMaxScale int

@description('The principal ID of the Clusters Service (CS) managed identity, sourced from the service resource group. Granted read/write on the per-management-cluster kube-applier CosmosDB container.')
param csManagedIdentityPrincipalId string

@description('The name of the mgmt-agent managed identity.')
param mgmtAgentMIName string

@description('The namespace of the mgmt-agent controller.')
param mgmtAgentNamespace string

@description('The service account name of the mgmt-agent controller.')
param mgmtAgentServiceAccountName string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('MSI that will be used to run deploymentScripts')
param globalMSIId string

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for hosted control planes)')
param hcpAzureMonitoringWorkspaceId string

// logs
@description('The namespace of the logs')
param logsNamespace string

@description('The managed identity name of the logs')
param logsMSI string

@description('The service account name of the logs managed identity')
param logsServiceAccount string

@description('Name of certificate in Keyvault and hostname used in SAN')
param genevaRpLogsName string

@description('Name of certificate in Keyvault and hostname used in SAN')
param genevaClusterLogsName string

@description('The name of the Azure Storage account to create for HCP Backups')
param hcpBackupsStorageAccountName string

@description('Event Hub name for AKS audit logs')
param auditLogsEventHubName string

@description('Resource ID of the event hub authorization rule for AKS audit logs')
param auditLogsEventHubAuthRuleId string

// The ManagedCluster resource + its node pools are created by the aks-cluster-create Go
// tool (dev-infrastructure/scripts/aks-cluster-create), which runs as its own pipeline
// step before this one -- see the "cluster-create" step in mgmt-pipeline.yaml. Everything
// below reads the cluster via an `existing` resource lookup rather than a bicep module
// output, and the workload-identity/ACR/RBAC wiring that used to live in
// aks-cluster-base.bicep (which this template used to call as a module) is now in
// modules/aks-cluster-post.bicep, called below.
resource aksCluster 'Microsoft.ContainerService/managedClusters@2026-04-02-preview' existing = {
  name: aksClusterName
}

//
//   M A N A G E D   I D E N T I T I E S
//

module managedIdentities '../modules/managed-identities.bicep' = {
  name: 'managed-identities'
  params: {
    location: location
    manageIdentityNames: [for wi in workloadIdentities: wi.value.uamiName]
  }
}

var workloadIdentities = items({
  maestro_wi: {
    uamiName: maestroConsumerMIName
    namespace: maestroConsumerNamespace
    serviceAccountName: maestroConsumerServiceAccountName
  }
  mgmt_agent_wi: {
    uamiName: mgmtAgentMIName
    namespace: mgmtAgentNamespace
    serviceAccountName: mgmtAgentServiceAccountName
  }
  logs_wi: {
    uamiName: logsMSI
    namespace: logsNamespace
    serviceAccountName: logsServiceAccount
  }
  prom_wi: {
    uamiName: 'prometheus'
    namespace: 'prometheus'
    serviceAccountName: 'prometheus'
  }
  velero_wi: {
    uamiName: 'velero'
    namespace: 'velero'
    serviceAccountName: 'velero'
  }
  kube_applier_wi: {
    uamiName: kubeApplierMIName
    namespace: kubeApplierNamespace
    serviceAccountName: kubeApplierServiceAccountName
  }
})

module aksPostConfig '../modules/aks-cluster-post.bicep' = {
  name: 'aks-cluster-post'
  params: {
    aksClusterName: aksClusterName
    location: location
    deploymentMsiId: globalMSIId
    pullAcrResourceIds: [ocpAcrResourceId, svcAcrResourceId]
    workloadIdentities: workloadIdentities
  }
  dependsOn: [
    managedIdentities
  ]
}

output aksClusterName string = aksClusterName

//
// M E T R I C S
//

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'prometheus'
  dependsOn: [
    managedIdentities
  ]
}

module dataCollection '../modules/metrics/datacollection.bicep' = {
  name: 'metrics-infra'
  params: {
    azureMonitorWorkspaceLocation: location
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    hcpAzureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    aksClusterName: aksClusterName
    prometheusPrincipalId: prometheusUAMI.properties.principalId
  }
}

// Declare this management cluster in the authoritative underlay-cluster inventory (see the module
// for details). Instantiated per stamp, so each management cluster emits its own series and they
// are torn down individually when a stamp is decommissioned.
module underlayClusterMetric '../modules/metrics/underlay-clusters-metric.bicep' = {
  name: 'underlay-clusters-metric'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    clusterName: aksClusterName
  }
}

//
// K E Y V A U L T S
//

resource logsUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: logsMSI
  dependsOn: [
    managedIdentities
  ]
}

module logsMgmtKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(mgmtKeyVaultName, logsMSI, 'certuser')
  params: {
    keyVaultName: mgmtKeyVaultName
    roleName: 'Key Vault Certificate User'
    managedIdentityPrincipalIds: [
      logsUAMI.properties.principalId
    ]
  }
}

module cxCSIKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, 'aks-kv-csi-mi', role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalIds: [aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId]
    }
  }
]

module msiCSIKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(msiKeyVaultName, 'aks-kv-csi-mi', role)
    params: {
      keyVaultName: msiKeyVaultName
      roleName: role
      managedIdentityPrincipalIds: [aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId]
    }
  }
]

resource mgmtKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: mgmtKeyVaultName
}

//
//   G E N E V A   C E R T I F I C A T E   A C C E S S
//

module genevaRpLogsCertCSIAccess '../modules/keyvault/key-vault-secret-access.bicep' = {
  name: 'geneva-mgmt-rp-certificate'
  params: {
    keyVaultName: mgmtKeyVaultName
    principalId: aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId
    secretName: genevaRpLogsName
  }
}

module genevaClusterLogsCertCSIAccess '../modules/keyvault/key-vault-secret-access.bicep' = {
  name: 'geneva-cluster-log-certificate'
  params: {
    keyVaultName: mgmtKeyVaultName
    principalId: aksCluster.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId
    secretName: genevaClusterLogsName
  }
}

//
//   M A E S T R O
//

resource maestroConsumerUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'maestro-consumer'
  dependsOn: [
    managedIdentities
  ]
}

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = {
  name: 'maestro-consumer'
  params: {
    maestroAgentManagedIdentityPrincipalId: maestroConsumerUAMI.properties.principalId
    maestroConsumerName: maestroConsumerName
    maestroEventGridNamespaceId: maestroEventGridNamespaceId
    certKeyVaultName: mgmtKeyVaultName
    certificateSAN: maestroConsumerCertSAN
    certificateIssuer: maestroCertIssuer
  }
  dependsOn: [
    mgmtKeyVault
  ]
}

//
//  E V E N T   G R I D   P R I V A T E   E N D P O I N T   C O N N E C T I O N
//

module eventGrindPrivateEndpoint '../modules/private-endpoint.bicep' = {
  name: 'eventGridPrivateEndpoint'
  params: {
    location: location
    subnetIds: [nodeSubnetId]
    privateLinkServiceId: maestroEventGridNamespaceId
    vnetId: vnetId
    serviceType: 'eventgrid'
    groupId: 'topicspace'
  }
}

//
//   K U B E   A P P L I E R
//

var rpCosmosDbAccountRef = res.cosmosDBAccountRefFromId(rpCosmosDbAccountId)

resource kubeApplierUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: kubeApplierMIName
  dependsOn: [
    managedIdentities
  ]
}

module kubeApplierCosmos '../modules/rp-cosmos-kube-applier.bicep' = if (rpCosmosDbAccountId != '') {
  name: 'kube-applier-cosmos-${uniqueString(resourceGroup().name)}'
  scope: resourceGroup(rpCosmosDbAccountRef.resourceGroup.subscriptionId, rpCosmosDbAccountRef.resourceGroup.name)
  params: {
    cosmosDBAccountName: rpCosmosDbAccountRef.name
    containerName: kubeApplierContainerName
    containerMaxScale: kubeApplierContainerMaxScale
    kubeApplierManagedIdentityPrincipalId: kubeApplierUAMI.properties.principalId
    csManagedIdentityPrincipalId: csManagedIdentityPrincipalId
  }
}

//
//  C O S M O S D B   P R I V A T E   E N D P O I N T   C O N N E C T I O N
//

module cosmosDbPrivateEndpoint '../modules/private-endpoint.bicep' = if (rpCosmosDbPrivate && rpCosmosDbAccountId != '') {
  name: 'cosmosDbPrivateEndpoint'
  params: {
    location: location
    subnetIds: [nodeSubnetId]
    privateLinkServiceId: rpCosmosDbAccountId
    vnetId: vnetId
    serviceType: 'cosmosdb'
    groupId: 'Sql'
  }
}

//
// O A D P  B A C K U P S
//

resource veleroUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'velero'
  dependsOn: [
    managedIdentities
  ]
}

module hcpBackupsRbac '../modules/hcp-backups/storage-rbac.bicep' = {
  name: 'hcp-backups-rbac'
  params: {
    storageAccountName: hcpBackupsStorageAccountName
    veleroManagedIdentityPrincipalId: veleroUAMI.properties.principalId
  }
}

//
//  A K S   D I A G N O S T I C   S E T T I N G S
//

module diagnosticSetting '../modules/aks/diagnostic-setting.bicep' = if (auditLogsEventHubAuthRuleId != '') {
  name: 'aks-diagnostic-setting'
  params: {
    aksClusterName: aksClusterName
    auditLogsEventHubName: auditLogsEventHubName
    auditLogsEventHubAuthRuleId: auditLogsEventHubAuthRuleId
  }
}
