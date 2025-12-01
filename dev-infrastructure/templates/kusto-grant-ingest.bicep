import * as res from '../modules/resource.bicep'

@description('Flag to indicate if arobit is enabled, used to check if permissions should be granted')
param arobitKustoEnabled bool

@description('Kusto resource ID')
param kustoResourceId string

@description('SVC Database name to grant access on')
param svcDatabaseName string

@description('Hcp Database name to grant access on')
param hcpDatabaseName string

@allowed(['mgmt', 'svc'])
@description('Define type of cluster, either mgmt or svc')
param clusterType string

@description('Principal IDs to grant access to')
param clusterLogPrincipalId string = ''
param adminApiPrincipalId string = ''

var hcpIngestors = clusterType == 'mgmt' ? [clusterLogPrincipalId] : []
var svcIngestors = [clusterLogPrincipalId]

var readers = clusterType == 'svc' ? [adminApiPrincipalId] : []

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
