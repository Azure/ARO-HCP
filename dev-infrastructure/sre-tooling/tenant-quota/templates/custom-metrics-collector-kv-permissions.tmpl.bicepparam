using '../templates/custom-metrics-collector-kv-permissions.bicep'

param msiClientId = '__msiClientId__'
param keyVaultName = '__keyVaultName__'
param keyVaultResourceGroup = '{{ .sretooling.rg }}'

