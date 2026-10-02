using '../templates/arobit-lookup.bicep'

import * as mi from '../modules/managed-identities.bicep'

param msiName = '{{ .logs.mdsd.msiName }}'
param infrastructureIdentityResourceGroup = {{ .infrastructureIdentities.useLeased }}
  ? mi.getManagementIdentityResourceGroup('{{ .infrastructureIdentities.managementResourceGroups }}', '{{ .mgmt.stampIdentifier }}')
  : '{{ .mgmt.rg }}'
