using '../templates/dev-ci-batch-analysis-storage.bicep'

param location = '{{ .opstool.batchAnalysis.storageAccount.location }}'
param storageAccountName = '{{ .opstool.batchAnalysis.storageAccount.name }}'
param containerName = '{{ .opstool.batchAnalysis.storageAccount.containerName }}'
param chaiPrincipalId = '{{ .opstool.batchAnalysis.chaiBot.principalId }}'
