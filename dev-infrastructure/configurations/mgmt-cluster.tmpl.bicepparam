using '../templates/mgmt-cluster.bicep'

// AKS
param aksClusterName = '{{ .mgmt.aks.name }}'
param nodeSubnetId = '__nodeSubnetId__'
param vnetId = '__vnetId__'

// Maestro
param maestroConsumerMIName = '{{ .maestro.agent.managedIdentityName }}'
param maestroConsumerNamespace = '{{ .maestro.agent.k8s.namespace }}'
param maestroConsumerServiceAccountName = '{{ .maestro.agent.k8s.serviceAccountName }}'

// Mgmt Agent
param mgmtAgentMIName = '{{ .mgmtAgent.managedIdentityName }}'
param mgmtAgentNamespace = '{{ .mgmtAgent.k8s.namespace }}'
param mgmtAgentServiceAccountName = '{{ .mgmtAgent.k8s.serviceAccountName }}'

param maestroConsumerName = '{{ .maestro.agent.consumerName }}'
param maestroConsumerCertSAN = '{{ .maestro.agent.certSAN }}'
param maestroCertIssuer = '{{ .maestro.certIssuer }}'
param maestroEventGridNamespaceId = '__maestroEventGridNamespaceId__'

// Kube Applier
param kubeApplierMIName = '{{ .kubeApplier.managedIdentityName }}'
param kubeApplierNamespace = '{{ .kubeApplier.k8s.namespace }}'
param kubeApplierServiceAccountName = '{{ .kubeApplier.k8s.serviceAccountName }}'
param kubeApplierContainerName = '{{ .kubeApplier.cosmosContainerName }}'
param kubeApplierContainerMaxScale = {{ .kubeApplier.cosmosContainerMaxScale }}
param rpCosmosDbAccountId = '__rpCosmosDbAccountId__'
param csManagedIdentityPrincipalId = '__csManagedIdentityPrincipalId__'
param rpCosmosDbPrivate = {{ .frontend.cosmosDB.private }}

// ACR
param ocpAcrResourceId = '__ocpAcrResourceId__'
param svcAcrResourceId = '__svcAcrResourceId__'

// CX KV
param cxKeyVaultName = '{{ .cxKeyVault.name }}'

// MSI KV
param msiKeyVaultName = '{{ .msiKeyVault.name }}'

// MGMT KV
param mgmtKeyVaultName = '{{ .mgmtKeyVault.name }}'

// MI for deployment scripts
param globalMSIId = '__globalMSIId__'

// Azure Monitor Workspace
param azureMonitoringWorkspaceId = '__azureMonitoringWorkspaceId__'
param hcpAzureMonitoringWorkspaceId = '__hcpAzureMonitoringWorkspaceId__'

// MDSD / Genevabits
param logsNamespace = '{{ .logs.mdsd.namespace }}'
param logsMSI = '{{ .logs.mdsd.msiName }}'
param logsServiceAccount = '{{ .logs.mdsd.serviceAccountName }}'

// Geneva logging settings
param genevaRpLogsName = '{{ .geneva.logs.rp.secretName }}'
param genevaClusterLogsName = '{{ .geneva.logs.cluster.secretName }}'

// HCP Backups Storage Account
param hcpBackupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccount.name }}'

// Audit Logs Event Hub
param auditLogsEventHubName = '{{ .auditLogsEventHub.name }}'
param auditLogsEventHubAuthRuleId = '__auditLogsEventHubAuthRuleId__'
