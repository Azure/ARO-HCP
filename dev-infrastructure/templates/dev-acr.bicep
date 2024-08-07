@minLength(5)
@maxLength(40)
@description('Globally unique name of the Azure Container Registry')
param acrName string

@description('Location of the registry.')
param location string = resourceGroup().location

@description('Service tier of the Azure Container Registry.')
param acrSku string

resource acrResource 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: acrName
  location: location
  sku: {
    name: acrSku
  }
  properties: {
    adminUserEnabled: false
    // Premium-only feature
    // https://azure.microsoft.com/en-us/blog/azure-container-registry-mitigating-data-exfiltration-with-dedicated-data-endpoints/
    dataEndpointEnabled: false
    encryption: {
      // The naming of this field is misleading - it disables encryption with a customer-managed key.
      // Data in Azure Container Registry is encrypted, just with an Azure-managed key.
      // https://learn.microsoft.com/en-us/azure/container-registry/tutorial-enable-customer-managed-keys#show-encryption-status
      status: 'disabled'
    }
  }
}

// https://learn.microsoft.com/en-us/azure/container-registry/container-registry-tasks-overview
resource acrPurgeTask 'Microsoft.ContainerRegistry/registries/tasks@2019-04-01' = {
  name: '${acrName}-purge'
  location: location
  parent: acrResource
  properties: {
    agentConfiguration: {
      cpu: 2
    }
    platform: {
      architecture: 'amd64'
      os: 'Linux'
    }
    step: {
      encodedTaskContent: base64('acr purge --filter "arohcpfrontend:.*" --keep 3 --ago 7d')
      type: 'EncodedTask'
    }
    timeout: 3600
    trigger: {
      timerTriggers: [
        {
          name: 'weekly'
          schedule: '0 0 * * *'
        }
      ]
    }
  }
}

@description('Login server property for later use')
output loginServer string = acrResource.properties.loginServer
