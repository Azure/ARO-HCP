using '../templates/output-svc.bicep'

param regionalResourceGroup = '{{ .regionRG }}'

// MSI Refresher
param msiRefresherMIName = '{{ .msiCredentialsRefresher.managedIdentityName }}'

// OIDC
param oidcStorageAccountName = '{{ .oidc.storageAccount.name }}'
param regionalAzureFrontDoortDnsZoneName = '{{ .dns.regionalSubdomain }}.{{ .oidc.frontdoor.subdomain }}.{{ .dns.svcParentZoneName }}'

// CS
param csPostgresServerName = '{{ .clustersService.postgres.name }}'
param csPostgresDatabaseName = '{{ .clustersService.postgres.databaseName }}'
param csPostgresDeploy = {{ .clustersService.postgres.deploy }}
param csMIName = '{{ .clustersService.managedIdentityName }}'

// Operator Roles
param opClusterApiAzureRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.clusterApiAzure.roleNames }}'
param opControlPlaneRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.controlPlane.roleNames }}'
param opCloudControllerManagerRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.cloudControllerManager.roleNames }}'
param opIngressRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.ingress.roleNames }}'
param opDiskCsiDriverRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.diskCsiDriver.roleNames }}'
param opFileCsiDriverRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.fileCsiDriver.roleNames }}'
param opImageRegistryRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.imageRegistry.roleNames }}'
param opCloudNetworkConfigRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.cloudNetworkConfig.roleNames }}'
param opKmsRoleName = '{{ .clustersService.azureOperatorsManagedIdentities.kms.roleNames }}'
