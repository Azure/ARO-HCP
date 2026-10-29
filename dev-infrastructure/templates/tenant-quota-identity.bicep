@description('Display name of the tenant-quota collector Entra application (config: opstool.tenantQuota.tenants.redHat0.applicationName)')
param applicationName string

@description('Comma-separated Entra object IDs added as owners of the application and service principal, so any OWNERS-group member can run the privileged pipeline. Existing owners are kept (append).')
param ownerIds string = ''

// Microsoft Graph app ID (constant across all tenants)
var msGraphAppId = '00000003-0000-0000-c000-000000000000'
// Organization.Read.All (Role / application permission); backs the directory quota
// collection through /v1.0/organization. Admin consent is granted by
// entra-app-credentials ensure-secret, as for the CI bots.
var organizationReadAllRoleId = '498476ce-e0fe-48b0-b801-37ba7e2685c6'

module collectorApp '../modules/entra/app.bicep' = {
  name: 'tenant-quota-collector-app'
  params: {
    applicationName: applicationName
    uniqueName: toLower(applicationName)
    manageSp: true
    ownerIds: ownerIds
    ownerRelationshipSemantics: 'append'
    requiredResourceAccess: [
      {
        resourceAppId: msGraphAppId
        resourceAccess: [
          {
            id: organizationReadAllRoleId
            type: 'Role'
          }
        ]
      }
    ]
  }
}
