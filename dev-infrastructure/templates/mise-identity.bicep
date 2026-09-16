param miseApplicationName string
param entraAppOwnerIds string
param miseApplicationDeploy bool

module miseApp '../modules/entra/app.bicep' = if (miseApplicationDeploy) {
  name: 'mise-entra-app'
  params: {
    applicationName: miseApplicationName
    ownerIds: entraAppOwnerIds
    manageSp: false
    serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
    isFallbackPublicClient: false
    requestedAccessTokenVersion: 2
  }
}
