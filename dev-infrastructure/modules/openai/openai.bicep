/*
This module creates an Azure OpenAI (Cognitive Services) resource for Holmes
GPT-powered cluster investigation. It includes:

- Azure OpenAI account with custom subdomain and Entra ID-only auth
- GPT model deployment (GlobalStandard SKU)

The holmesgpt managed identity role assignment is handled separately in
mgmt-cluster.bicep because the MSI is created per management cluster.

Execution scope: the regional resource group
*/

@description('Azure Region Location')
param location string

@description('The name of the Azure OpenAI account')
param aoaiName string

@description('The model name to deploy')
param aoaiModelName string = 'gpt-5.2'

@description('The model version to deploy')
param aoaiModelVersion string = '2025-12-11'

@description('The deployment SKU capacity (tokens per minute in thousands)')
param aoaiDeploymentCapacity int = 10

//
//   A Z U R E   O P E N A I   A C C O U N T
//

resource aoaiAccount 'Microsoft.CognitiveServices/accounts@2024-10-01' = {
  name: aoaiName
  location: location
  kind: 'OpenAI'
  sku: {
    name: 'S0'
  }
  properties: {
    customSubDomainName: aoaiName
    disableLocalAuth: true
    publicNetworkAccess: 'Enabled'
  }
}

//
//   M O D E L   D E P L O Y M E N T
//

resource aoaiDeployment 'Microsoft.CognitiveServices/accounts/deployments@2024-10-01' = {
  parent: aoaiAccount
  name: aoaiModelName
  sku: {
    name: 'DataZoneStandard'
    capacity: aoaiDeploymentCapacity
  }
  properties: {
    model: {
      format: 'OpenAI'
      name: aoaiModelName
      version: aoaiModelVersion
    }
  }
}

output aoaiEndpoint string = aoaiAccount.properties.endpoint
output aoaiResourceId string = aoaiAccount.id
