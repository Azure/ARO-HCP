using '../dev-ci/cluster/opstool-kusto-grant.bicep'

param managedIdentityName = 'tenant-quota'
param kustoName = '{{ .opstool.tenantQuota.ciJobOutcomes.kustoName }}'
param kustoResourceGroup = '{{ .opstool.tenantQuota.ciJobOutcomes.kustoResourceGroup }}'
param databaseName = '{{ .opstool.tenantQuota.ciJobOutcomes.database }}'
