using '../templates/mock-identities.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'

param keyVaultName = '{{ .serviceKeyVault.name }}'
