import { getGeoShortForRegion } from '../../common.bicep'

param clusterLogManagedIdentityId string
param clusterLocation string

param databaseName string
var kustoName = 'hcp-${getGeoShortForRegion(clusterLocation)}'

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' existing = {
  name: '${kustoName}/${databaseName}'
}

resource grantSVCIngest 'Microsoft.Kusto/clusters/databases/principalAssignments@2024-04-13' = {
  parent: database
  name: 'grant-${guid(clusterLogManagedIdentityId, databaseName)}'
  properties: {
    principalId: clusterLogManagedIdentityId
    principalType: 'App'
    role: 'Ingestor'
    tenantId: tenant().tenantId
  }
}
