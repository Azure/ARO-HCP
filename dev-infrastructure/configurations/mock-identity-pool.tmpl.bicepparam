using '../templates/mock-identity-pool.bicep'

param globalMSIName = '{{ .global.globalMSIName }}'

param keyVaultName = '{{ .serviceKeyVault.name }}'
