@description('Graph uniqueName of the Entra application to look up (the normalized name — lowercase, spaces replaced with dashes — NOT the display name).')
param applicationName string
param manage bool

@description('When true, also look up the service principal and output its principalId')
param lookupSp bool = false

extension microsoftGraphBeta

resource entraApp 'Microsoft.Graph/applications@beta' existing = if (manage) {
  uniqueName: applicationName
}

resource entraSp 'Microsoft.Graph/servicePrincipals@beta' existing = if (manage && lookupSp) {
  #disable-next-line BCP318
  appId: entraApp.appId
}

#disable-next-line BCP318
output appId string = manage ? entraApp.appId : ''
output tenantId string = tenant().tenantId

#disable-next-line BCP318
output principalId string = manage && lookupSp ? entraSp.id : ''
