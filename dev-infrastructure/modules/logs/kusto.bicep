/*
This module creates an Azure Data Explorer (Kusto) cluster with managed identity,
database, and appropriate role assignments.
Use for Dev only, in MSFT einvironments this is manged by MSFT
*/

@description('The name of the Kusto cluster.')
param clusterName string

@description('The location for the Kusto cluster.')
param location string = resourceGroup().location

@description('The capacity (number of instances) for the Kusto cluster.')
@minValue(1)
@maxValue(3)
param capacity int = 1

@description('Soft delete period for the database (ISO 8601 duration)')
param softDeletePeriod string = 'P7D'

@description('Hot cache period for the database (ISO 8601 duration)')
param hotCachePeriod string = 'P2D'

resource kustoCluster 'Microsoft.Kusto/clusters@2024-04-13' = {
  name: clusterName
  location: location
  sku: {
    name: 'Dev(No SLA)_Standard_D11_v2'
    capacity: capacity
    tier: 'Basic'
  }
  properties: {
    enablePurge: true
    publicIPType: 'IPv4'
  }
}

resource serviceLogs 'Microsoft.Kusto/clusters/databases@2024-04-13' = {
  parent: kustoCluster
  location: location
  name: 'HCPServiceLogs'
  kind: 'ReadWrite'

  properties: {
    hotCachePeriod: hotCachePeriod
    softDeletePeriod: softDeletePeriod
  }

  resource dbAdmin 'principalAssignments' = {
    name: 'dbAdmin'
    properties: {
      principalId: 'aro-hcp-engineering-App Developer'
      principalType: 'Group'
      role: 'Admin'
      tenantId: tenant().tenantId
    }
  }
  resource servicesLogs 'scripts' = {
    name: 'servicesLogs'
    properties: {
      #disable-next-line use-secure-value-for-secure-inputs
      scriptContent: loadTextContent('servicesLogs.kql')
      continueOnErrors: false
    }
  }
}

resource customerLogs 'Microsoft.Kusto/clusters/databases@2024-04-13' = {
  parent: kustoCluster
  location: location
  name: 'HCPCustomerLogs'
  kind: 'ReadWrite'

  properties: {
    hotCachePeriod: hotCachePeriod
    softDeletePeriod: softDeletePeriod
  }

  resource dbAdmin 'principalAssignments' = {
    name: 'dbAdmin'
    properties: {
      principalId: 'aro-hcp-engineering-App Developer'
      principalType: 'Group'
      role: 'Admin'
      tenantId: tenant().tenantId
    }
  }

  resource customerLogs 'scripts' = {
    name: 'customerLogs'
    properties: {
      #disable-next-line use-secure-value-for-secure-inputs
      scriptContent: loadTextContent('customerLogs.kql')
      continueOnErrors: false
    }
  }
}
