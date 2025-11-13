param genevaActionApplicationName string
param genevaActionApplicationManage bool

extension microsoftGraphBeta

resource genevaActionsApp 'Microsoft.Graph/applications@beta' existing = if (genevaActionApplicationManage) {
  uniqueName: genevaActionApplicationName
}

output genevaActionsAppId string = genevaActionApplicationManage ? genevaActionsApp.appId : ''
output tenantId string = tenant().tenantId
