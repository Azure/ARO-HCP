@description('Azure region for all resources')
param location string

@description('Name of the Function App')
param functionAppName string

@description('Name of the App Service Plan')
param appServicePlanName string

@description('Name of the Storage Account for Function App runtime')
param storageAccountName string

@description('Name of the User-Assigned Managed Identity')
param managedIdentityName string

@description('Key Vault URL for Geneva Health certificate')
param svcKeyVaultUrl string

@description('Key Vault certificate name for Geneva Health')
param keyVaultCertificateName string

@description('Geneva Health monitoring account name')
param genevaMonitoringAccount string

@description('Geneva Health ExecutionEnvironment (Int, Prod, UsNat, UsSec)')
param genevaHealthEnvironment string

@description('HCP AMW Prometheus query endpoint')
param hcpMonitorPrometheusQueryEndpoint string

@description('Name of the service Key Vault containing the certificate')
param svcKeyVaultName string

@description('Subscription of the service Key Vault resource group')
param serviceKeyVaultSubscription string = subscription().subscriptionId

@description('Resource group of the service Key Vault')
param svcKeyVaultResourceGroup string = resourceGroup().name

@description('Default region value for Geneva watchdog reports')
param genevaDefaultRegion string

@description('Container image for the Function App (e.g. myacr.azurecr.io/geneva-health-function:latest)')
param containerImage string

// ── Managed Identity ──
resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: managedIdentityName
  location: location
}

// ── Storage Account (required by Function App runtime) ──
resource storageAccount 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: storageAccountName
  location: location
  kind: 'StorageV2'
  sku: {
    name: 'Standard_LRS'
  }
  properties: {
    supportsHttpsTrafficOnly: true
    minimumTlsVersion: 'TLS1_2'
  }
}

// ── App Service Plan (Linux Elastic Premium for container support) ──
resource appServicePlan 'Microsoft.Web/serverfarms@2023-12-01' = {
  name: appServicePlanName
  location: location
  kind: 'linux'
  sku: {
    name: 'EP1'
    tier: 'ElasticPremium'
  }
  properties: {
    reserved: true
  }
}

// ── Function App ──
resource functionApp 'Microsoft.Web/sites@2023-12-01' = {
  name: functionAppName
  location: location
  kind: 'functionapp,linux,container'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${managedIdentity.id}': {}
    }
  }
  properties: {
    serverFarmId: appServicePlan.id
    httpsOnly: true
    siteConfig: {
      linuxFxVersion: 'DOCKER|${containerImage}'
      appSettings: [
        {
          name: 'AzureWebJobsStorage'
          value: 'DefaultEndpointsProtocol=https;AccountName=${storageAccount.name};EndpointSuffix=${environment().suffixes.storage};AccountKey=${storageAccount.listKeys().keys[0].value}'
        }
        {
          name: 'FUNCTIONS_EXTENSION_VERSION'
          value: '~4'
        }
        {
          name: 'AZURE_CLIENT_ID'
          value: managedIdentity.properties.clientId
        }
        {
          name: 'Geneva__KeyVaultUrl'
          value: svcKeyVaultUrl
        }
        {
          name: 'Geneva__KeyVaultCertificateName'
          value: keyVaultCertificateName
        }
        {
          name: 'Geneva__MonitoringAccountName'
          value: genevaMonitoringAccount
        }
        {
          name: 'Geneva__Environment'
          value: genevaHealthEnvironment
        }
        {
          name: 'Geneva__DefaultRegion'
          value: genevaDefaultRegion
        }
        {
          name: 'Amw__PrometheusQueryEndpoint'
          value: hcpMonitorPrometheusQueryEndpoint
        }
      ]
    }
  }
}

// ── Key Vault access: grant managed identity Secrets User on the certificate ──
module certAccess '../keyvault/key-vault-secret-access.bicep' = {
  name: 'geneva-health-cert-access-${uniqueString(managedIdentityName)}'
  scope: resourceGroup(serviceKeyVaultSubscription, svcKeyVaultResourceGroup)
  params: {
    keyVaultName: svcKeyVaultName
    secretName: keyVaultCertificateName
    principalId: managedIdentity.properties.principalId
  }
}


output functionAppName string = functionApp.name
output functionAppDefaultHostName string = functionApp.properties.defaultHostName
output managedIdentityPrincipalId string = managedIdentity.properties.principalId
output managedIdentityClientId string = managedIdentity.properties.clientId
