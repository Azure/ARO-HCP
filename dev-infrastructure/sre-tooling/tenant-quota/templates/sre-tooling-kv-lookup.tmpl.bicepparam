using '../templates/sre-tooling-kv-lookup.bicep'

// The sre-tooling Key Vault name pattern: ah-{env}-tool-{regionShort}-1
// For pers dev westus3: ah-pers-tool-usw3trwi-1
// We'll look it up by querying Key Vaults in the resource group
// For now, we construct it - in the future this could come from infrastructure outputs
param keyVaultName = 'ah-pers-tool-usw3trwi-1'
param keyVaultResourceGroup = '{{ .sretooling.rg }}'

