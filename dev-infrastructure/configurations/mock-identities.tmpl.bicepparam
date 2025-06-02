using '../templates/mock-identities.bicep'

param globalMsiName = '{{ .global.globalMSIName }}'

param keyVaultName = '{{ .serviceKeyVault.name }}'
