import * as res from '../modules/resource.bicep'

@description('Flag to indicate if arobit is enabled, used to check if permissions should be granted')
param arobitKustoEnabled bool

@description('Kusto resource ID')
param kustoResourceId string

@description('SVC Database name to grant access on')
param svcDatabaseName string

@description('Hcp Database name to grant access on')
param hcpDatabaseName string

@description('Unified AllAroHcpLogs database name to grant access on')
param allAroHcpLogsDatabaseName string

@allowed(['mgmt', 'svc'])
@description('Define type of cluster, either mgmt or svc')
param clusterType string

@description('Principal IDs to grant access to')
param clusterLogPrincipalId string = ''
param adminApiPrincipalId string = ''
param exporterPrincipalId string = ''

var hcpIngestors = clusterType == 'mgmt' ? [clusterLogPrincipalId] : []
var svcIngestors = [clusterLogPrincipalId]

// The arobit forwarder writes all logs to the unified AllAroHcpLogs database,
// regardless of cluster type, so its principal must be an ingestor there.
var allAroHcpLogsIngestors = [clusterLogPrincipalId]

var readers = clusterType == 'svc' ? [adminApiPrincipalId, exporterPrincipalId] : []

var kustoRef = res.kustoRefFromId(kustoResourceId)

module grantKustoSvcDB '../modules/logs/kusto/grant-access.bicep' = if (arobitKustoEnabled && kustoResourceId != '') {
  name: 'grantKusto-svc-${uniqueString(resourceGroup().name)}'
  params: {
    ingestAccessPrincipalIds: svcIngestors
    readAccessPrincipalIds: readers
    databaseName: svcDatabaseName
    kustoName: kustoRef.name
  }
  scope: resourceGroup(kustoRef.resourceGroup.subscriptionId, kustoRef.resourceGroup.name)
}

module grantKustoHcpDB '../modules/logs/kusto/grant-access.bicep' = if (arobitKustoEnabled && kustoResourceId != '') {
  name: 'grantKusto-hcp-${uniqueString(resourceGroup().name)}'
  params: {
    ingestAccessPrincipalIds: hcpIngestors
    readAccessPrincipalIds: readers
    databaseName: hcpDatabaseName
    kustoName: kustoRef.name
  }
  scope: resourceGroup(kustoRef.resourceGroup.subscriptionId, kustoRef.resourceGroup.name)
}

module grantKustoAllAroHcpLogsDB '../modules/logs/kusto/grant-access.bicep' = if (arobitKustoEnabled && kustoResourceId != '') {
  name: 'grantKusto-allarohcp-${uniqueString(resourceGroup().name)}'
  params: {
    ingestAccessPrincipalIds: allAroHcpLogsIngestors
    readAccessPrincipalIds: readers
    databaseName: allAroHcpLogsDatabaseName
    kustoName: kustoRef.name
  }
  scope: resourceGroup(kustoRef.resourceGroup.subscriptionId, kustoRef.resourceGroup.name)
}
