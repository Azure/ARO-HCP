param location string
param oidcIssuerUrl string
param workloadIdentities array

resource leasedUami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = [
  for wi in workloadIdentities: {
    name: wi.value.uamiName
  }
]

resource leasedUamiFedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(workloadIdentities)): {
    parent: leasedUami[i]
    name: '${workloadIdentities[i].value.uamiName}-${location}-fedcred'
    properties: {
      audiences: [
        'api://AzureADTokenExchange'
      ]
      issuer: oidcIssuerUrl
      subject: 'system:serviceaccount:${workloadIdentities[i].value.namespace}:${workloadIdentities[i].value.serviceAccountName}'
    }
  }
]

resource leasedPullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'image-puller'
}

@batchSize(1)
resource leasedPullerFedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(workloadIdentities)): {
    parent: leasedPullerIdentity
    name: '${workloadIdentities[i].value.uamiName}-${location}-puller-fedcred'
    properties: {
      audiences: [
        'api://AzureCRTokenExchange'
      ]
      issuer: oidcIssuerUrl
      subject: 'system:serviceaccount:${workloadIdentities[i].value.namespace}:${workloadIdentities[i].value.serviceAccountName}'
    }
  }
]
