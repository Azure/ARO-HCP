using '../templates/output-keyvault-secret.bicep'

param keyVaultName = '{{ .opstool.keyVault.name }}'
param secretName = '{{ .opstool.cihealthAuth.authProxy.clientSecretKeyVaultSecretName }}'
