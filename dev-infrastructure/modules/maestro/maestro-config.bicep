/*
This is a module for generating consistent constants and names for resources
that are shared across the maestro-server and (upcoming) maestro-consumer modules.
*/

@description('The resource group name for the Maestro infrastructure')
param resourceGroupName string

@description('The location for the Maestro infrastructure')
param location string

@description('The Maestro Event Grid Namespaces name')
param eventGridNamespaceName string?

@description('The name for the Key Vault for Maestro certificates')
param keyVaultName string = take('maestro-kv-${location}-${uniqueString(resourceGroupName)}', 24)

@description('The base domain name used in the the Event Grid certificate')
param certificateDomain string?

output kvCertOfficerManagedIdentityName string = '${keyVaultName}-cert-officer-msi'

output maestroEventGridNamespaceName string = eventGridNamespaceName ?? '${resourceGroupName}-eventgrid'

output maestroKeyVaultName string = keyVaultName

output maestroCertificateDomain string = certificateDomain ?? '${location}.maestro.keyvault.aro-int.azure.com'
