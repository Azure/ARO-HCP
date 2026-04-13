// Kusto (ADX) log search alerting infrastructure.
// Creates shared resources (identity, permissions) and delegates
// alert definitions to alert-rules.bicep.
// ICM action group is provided by the monitoring pipeline (SL action group).
// Deployed as part of the Kusto infrastructure in the kusto RG.

@description('Resource ID of the ADX cluster')
param kustoClusterId string

@description('Resource ID of the SL ICM action group (empty string if not available)')
param slActionGroupId string

@description('Name of the service logs database')
param serviceLogsDatabase string

@description('Name of the hosted control plane logs database')
param hostedControlPlaneLogsDatabase string

@description('Name of the Kusto cluster')
param kustoName string

@description('URI of the ADX cluster (e.g. https://cluster.region.kusto.windows.net)')
param kustoUri string

@description('Azure region')
param location string = resourceGroup().location

// 1. User-assigned managed identity (shared by all Kusto alert rules)
resource alertIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'kusto-alerts-identity'
  location: location
}

// 2. Azure RBAC Reader on ADX cluster (required by scheduledQueryRules to validate access)
resource kustoCluster 'Microsoft.Kusto/clusters@2024-04-13' existing = {
  name: kustoName
}

var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

resource kustoReaderRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(kustoCluster.id, alertIdentity.id, readerRoleId)
  scope: kustoCluster
  properties: {
    principalId: alertIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

// 3. Grant Viewer access on ADX databases for the alert identity (for KQL query execution)
module serviceLogsAccess '../logs/kusto/grant-access.bicep' = {
  name: 'kusto-alerts-servicelogs-access'
  params: {
    kustoName: kustoName
    databaseName: serviceLogsDatabase
    readAccessPrincipalIds: [alertIdentity.properties.principalId]
  }
}

module hcpLogsAccess '../logs/kusto/grant-access.bicep' = {
  name: 'kusto-alerts-hcplogs-access'
  params: {
    kustoName: kustoName
    databaseName: hostedControlPlaneLogsDatabase
    readAccessPrincipalIds: [alertIdentity.properties.principalId]
  }
}

// 4. Action group IDs — reuse ICM SL action group from monitoring pipeline
var actionGroupIds = slActionGroupId != '' ? [slActionGroupId] : []

// 5. Alert rules — add new alerts in alert-rules.bicep
module alertRules 'alert-rules.bicep' = {
  name: 'kusto-alert-rules'
  params: {
    location: location
    kustoClusterId: kustoClusterId
    actionGroupIds: actionGroupIds
    identityId: alertIdentity.id
    adxServiceLogs: '${kustoUri}/${serviceLogsDatabase}'
  }
  dependsOn: [serviceLogsAccess, hcpLogsAccess, kustoReaderRole]
}
