@description('Whether ICM action groups are managed in this environment')
param manageConnection bool

resource icmSRE 'Microsoft.Insights/actionGroups@2024-10-01-preview' existing = if (manageConnection) {
  name: 'icm-action-group-sre'
}

resource icmSL 'Microsoft.Insights/actionGroups@2024-10-01-preview' existing = if (manageConnection) {
  name: 'icm-action-group-sl'
}

resource icmRP 'Microsoft.Insights/actionGroups@2024-10-01-preview' existing = if (manageConnection) {
  name: 'icm-action-group-rp'
}

resource icmMSFT 'Microsoft.Insights/actionGroups@2024-10-01-preview' existing = if (manageConnection) {
  name: 'icm-action-group-msft'
}

output actionGroupsSL string = manageConnection ? icmSL.id : ''
output actionGroupsSRE string = manageConnection ? icmSRE.id : ''
output actionGroupsRP string = manageConnection ? icmRP.id : ''
output actionGroupsMSFT string = manageConnection ? icmMSFT.id : ''
