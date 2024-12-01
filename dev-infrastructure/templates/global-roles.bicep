@description('Defines if the ACR token management role should be created')
param manageTokenRole bool

module tokenMgmtRole '../modules/acr/token-mgmt-role.bicep' = if (manageTokenRole) {
  name: 'acr-token-mgmt-role'
  scope: subscription()
}
