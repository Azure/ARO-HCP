param location string
param oidcIssuerUrl string
param workloadIdentities array

// pullOnly workload identities have no managed identity of their own, they only
// get a federated credential on the image-puller identity
var managedWorkloadIdentities = filter(workloadIdentities, wi => !(wi.value.?pullOnly ?? false))

resource leasedUami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = [
  for wi in managedWorkloadIdentities: {
    name: wi.value.uamiName
  }
]

// Federates each workload's own managed identity with its service account, so the
// workload can authenticate to Azure. Runs over managedWorkloadIdentities only.
resource leasedUamiFedcred 'Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials@2023-01-31' = [
  for i in range(0, length(managedWorkloadIdentities)): {
    parent: leasedUami[i]
    name: '${managedWorkloadIdentities[i].value.uamiName}-${location}-fedcred'
    properties: {
      audiences: [
        'api://AzureADTokenExchange'
      ]
      issuer: oidcIssuerUrl
      subject: 'system:serviceaccount:${managedWorkloadIdentities[i].value.namespace}:${managedWorkloadIdentities[i].value.serviceAccountName}'
    }
  }
]

resource leasedPullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'image-puller'
}

// Federates the shared image-puller identity with each service account, so the ACR pull
// controller can mint pull secrets for it. Runs over all workloadIdentities, pullOnly included.
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
