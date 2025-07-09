@description('The name of the front door profile')
param frontDoorProfileName string

@description('The name of the front door endpoint')
param frontDoorEndpointName string

@description('The name of the DNS zone')
param zoneName string

@description('The custom domain name to associate with front door endpoint')
param customDomainName string

@description('The name of the front door route')
param routeName string

@description('The name of the front door origin group')
param originGroupName string

@description('The name of the front door origin')
param originName string

// Front door private link location should be one of these https://learn.microsoft.com/en-us/azure/frontdoor/private-link#region-availability
@description('The location of the front door private link')
param privateLinkLocation string

@description('The message to send when a private link is created to storage')
param requestMessage string

@description('The name of the Azure Storage account to create')
param storageName string

@description('The Azure Storage account resource group')
param storageResourceGroup string

@description('The Azure Storage account subscription')
param storageSubscription string

@description('The name of the Key Vault that contains the custom domain\'s certificate.')
param keyVaultName string

@description('The name of the Key Vault secret that contains the custom domain\'s certificate.')
param certificateName string

var useManagedCertificates = empty(keyVaultName)

resource zone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: zoneName
}

resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' existing = {
  name: frontDoorProfileName
}

resource frontDoorEndpoint 'Microsoft.Cdn/profiles/afdEndpoints@2023-05-01' existing = {
  name: frontDoorEndpointName
  parent: frontDoorProfile
}

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' existing = {
  name: storageName
  scope: resourceGroup(storageSubscription, storageResourceGroup)
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = if (!useManagedCertificates) {
  name: keyVaultName

  resource secret 'secrets' existing = {
    name: certificateName
  }
}

resource secret 'Microsoft.Cdn/profiles/secrets@2023-05-01' = if (!useManagedCertificates) {
  name: '${keyVaultName}-${certificateName}-latest'
  parent: frontDoorProfile
  properties: {
    parameters: {
      type: 'CustomerCertificate'
      useLatestVersion: true
      secretSource: {
        id: keyVault::secret.id
      }
    }
  }
}

resource customDomain 'Microsoft.Cdn/profiles/customDomains@2023-05-01' = {
  name: customDomainName
  parent: frontDoorProfile
  properties: {
    hostName: '${customDomainName}.${zoneName}'
    tlsSettings: {
      minimumTlsVersion: 'TLS12'
      certificateType: useManagedCertificates ? 'ManagedCertificate' : 'CustomerCertificate'
      secret: useManagedCertificates
        ? null
        : {
            id: secret.id
          }
    }
    azureDnsZone: {
      id: zone.id
    }
  }
  dependsOn: [
    zone
  ]
}

resource cnameRecord 'Microsoft.Network/dnsZones/CNAME@2018-05-01' = {
  parent: zone
  name: customDomainName
  properties: {
    TTL: 3600
    CNAMERecord: {
      cname: frontDoorEndpoint.properties.hostName
    }
  }
}

resource validationTxtRecord 'Microsoft.Network/dnsZones/TXT@2018-05-01' = if (useManagedCertificates) {
  parent: zone
  name: '_dnsauth.${customDomainName}'
  properties: {
    TTL: 3600
    TXTRecords: [
      {
        value: [
          customDomain.properties.validationProperties.validationToken
        ]
      }
    ]
  }
}

resource route 'Microsoft.Cdn/profiles/afdEndpoints/routes@2023-05-01' = {
  name: routeName
  parent: frontDoorEndpoint
  properties: {
    customDomains: [
      {
        id: customDomain.id
      }
    ]
    originGroup: {
      id: originGroup.id
    }
    supportedProtocols: [
      'Http'
      'Https'
    ]
    patternsToMatch: [
      '/*'
    ]
    forwardingProtocol: 'HttpsOnly'
    linkToDefaultDomain: 'Disabled'
    httpsRedirect: 'Enabled'
  }
  dependsOn: [
    origin
  ]
}

resource originGroup 'Microsoft.Cdn/profiles/originGroups@2023-05-01' = {
  name: originGroupName
  parent: frontDoorProfile
  properties: {
    loadBalancingSettings: {
      sampleSize: 4
      successfulSamplesRequired: 2
      additionalLatencyInMilliseconds: 50
    }
    sessionAffinityState: 'Disabled'
  }
}

resource origin 'Microsoft.Cdn/profiles/originGroups/origins@2023-05-01' = {
  name: originName
  parent: originGroup
  properties: {
    hostName: replace(replace(storageAccount.properties.primaryEndpoints.web, 'https://', ''), '/', '')
    httpPort: 80
    httpsPort: 443
    originHostHeader: replace(replace(storageAccount.properties.primaryEndpoints.web, 'https://', ''), '/', '')
    priority: 1
    weight: 1000
    enabledState: 'Enabled'
    sharedPrivateLinkResource: {
      privateLink: {
        id: storageAccount.id
      }
      groupId: 'web'
      privateLinkLocation: privateLinkLocation
      requestMessage: requestMessage
    }
    enforceCertificateNameCheck: true
  }
}
