param applicationName string
param manage bool

@description('When true, also look up the service principal and output its principalId')
param lookupSp bool = false

extension microsoftGraphBeta

resource entraApp 'Microsoft.Graph/applications@beta' existing = if (manage) {
  uniqueName: applicationName
}

resource entraSp 'Microsoft.Graph/servicePrincipals@beta' existing = if (lookupSp) {
  appId: entraApp.appId
}

output appId string = manage ? entraApp.appId : ''
output tenantId string = tenant().tenantId

#disable-next-line BCP318
output principalId string = lookupSp ? entraSp.id : ''
