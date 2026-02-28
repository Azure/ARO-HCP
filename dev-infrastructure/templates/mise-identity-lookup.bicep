param miseApplicationName string
param miseApplicationDeploy bool

extension microsoftGraphBeta

resource miseApp 'Microsoft.Graph/applications@beta' existing = if (miseApplicationDeploy) {
  uniqueName: miseApplicationName
}

output miseAppId string = miseApplicationDeploy ? miseApp.appId : ''
