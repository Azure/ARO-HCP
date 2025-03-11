## Readme

This folder contains public keys for keys created in Azure Key Vaults.

These keys are used for assymetric encryption. See [https://github.com/Azure/ARO-HCP/tree/main/tooling/secret-sync](https://github.com/Azure/ARO-HCP/tree/main/tooling/secret-sync)

Naming convention:

`$environment_$region_$keyvault_$key.pem`

Explanation
- Environment: Name of environment
- Region: Region name, or global for global resources
- Keyvault: logical keyvault name without pre/suffixes
- Key: Name of the key within the keyvault
