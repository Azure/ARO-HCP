using '../templates/deployment-script-storage.bicep'

param deploymentScriptStorageAccountName = '{{ .serviceKeyVault.deploymentScriptStorageAccountName }}'
param globalMSIId = '__globalMSIId__'
