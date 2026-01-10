using '../templates/sre-tooling-infra.bicep'

// These will be overridden via command line in Makefile
param serviceKeyVaultName = ''
param serviceKeyVaultResourceGroup = ''
param serviceKeyVaultLocation = 'westus3'
param serviceKeyVaultSoftDelete = true
param serviceKeyVaultPrivate = true
param serviceKeyVaultTagName = 'aro-hcp-environment'
param serviceKeyVaultTagValue = 'dev'
param globalMSIId = ''
param kvCertOfficerPrincipalId = ''

