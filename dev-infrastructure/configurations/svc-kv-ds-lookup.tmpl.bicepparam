using '../templates/deployment-script-storage-lookup.bicep'

param deploymentScriptStorageAccountName = '{{ .serviceKeyVault.deploymentScriptStorageAccountName }}'
