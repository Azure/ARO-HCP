# MSIT INT Credential Setup

This document provides instructions for setting up **first-party**, **MSI mock**, and **ARM helper** credentials for the MSIT INT environment.

## Prerequisites
### IAM required
#### ARO SRE Team - INT (EA Subscription 3)
- Key Vault Administrator
- Contributor

#### Azure Red Hat OpenShift v4.x - HCP
- Contributor
- Key Vault Administrator (can only be obtained through PIM if you are in `tm-aro-engineering`)

## Overview
The MSIT INT environment is unique because the first-party, MSI mock, and ARM helper credentials exist outside the MSIT subscription. Therefore, some manual steps are required to configure the environment.

1. **Authenticate and set your subscription**

   ```bash
   az account set -n "ARO SRE Team - INT (EA Subscription 3)"
   ```

1. **ONLY PERFORM THIS STEP IF NEEDED**. Create the global resource group and keyvault in the `ARO SRE Team - INT (EA Subscription 3)`.  This is not automated so create the global rg and keyvault (`aro-hcp-int-kv`) manually.

1. **Create the INT mock identity certificates**
   The INT mock identity Entra apps and service principals are created
   declaratively by `templates/mock-identity-apps.bicep` (the `mock-identity-apps-int`
   step of the Owner-only `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint,
   run with `make dev-ci-privileged-local-run`). That template configures the apps
   for SNI certificate authentication but does **not** create the certificates.

   Create the three certificates in the `aro-hcp-int-kv` Key Vault with the
   dedicated target (idempotent — existing certs are left untouched):

   ```bash
   cd dev-infrastructure/
   make create-int-mock-identity-certs
   ```

   This runs `scripts/create-kv-cert.sh` for `intFirstPartyCert`,
   `intArmHelperCert`, and `intMsiMockCert`, with the subject/DNS names that match
   `.ci.int.mockIdentities.*.certDns` in `config/config-dev-ci.yaml`. Because the
   apps use SNI, the certificates can be rotated later without redeploying the
   Bicep, as long as the subject name is unchanged.

1. **Update configuration**
   If new Entra apps were created, update the configuration, see [configuration](../configuration.md) for details about that process.  You can read the created client IDs with `az ad app list --display-name <applicationName> --query '[0].appId'` for each `.ci.int.mockIdentities.*.applicationName`.
   ```
    firstPartyAppClientId: b3cb2fab-15cb-4583-ad06-f91da9bfe2d1
    firstPartyAppCertificate:
      name: intFirstPartyCert
      manage: false # we have the cert from RH for int
    # Mock Managed Identities Service Principal - from RH Tenant
    miMockClientId: e8723db7-9b9e-46a4-9f7d-64d75c3534f0
    miMockPrincipalId: d6b62dfa-87f5-49b3-bbcb-4a687c4faa96
    miMockCertName: intMsiMockCert
    # ARM Helper - from RH Tenant
    armHelperClientId: 3331e670-0804-48e8-a086-6241671ddc93
    armHelperFPAPrincipalId: 47f69502-0065-4d9a-b19b-d403e183d2f4
    armHelperCertName: intArmHelperCert
   ```

1. **Download** the certificates from the `aro-hcp-int-kv`
   ```bash
   # List the certificates in the Key Vault
   az keyvault certificate list -o table --vault-name aro-hcp-int-kv
   # Example output:
   # Name               Subject    X509Thumbprint                X509ThumbprintHex
   # -----------------  ---------  ----------------------------  ----------------------------------------
   # intArmHelperCert              34+RQPaIVjyr0Gp4qRfMSu6OUfw=  DF8F9140F688563CABD06A78A917CC4AEE8E51FC
   # intFirstPartyCert             g8MBUq+v089XXlnS2GMqPLLdmAg=  83C30152AFAFD3CF575E59D2D8632A3CB2DD9808
   # intMsiMockCert                ifvf/t2EyZhNDwE3KR85QmU8cC8=  89FBDFFEDD84C9984D0F0137291F3942653C702F

   # Download the certificate bundles
   az keyvault secret download --vault-name aro-hcp-int-kv --name intArmHelperCert --file intArmHelperCert
   cat intArmHelperCert | base64 -d > intArmHelperCert.pfx

   az keyvault secret download --vault-name aro-hcp-int-kv --name intFirstPartyCert --file intFirstPartyCert
   cat intFirstPartyCert | base64 -d > intFirstPartyCert.pfx

   az keyvault secret download --vault-name aro-hcp-int-kv --name intMsiMockCert --file intMsiMockCert
   cat intMsiMockCert | base64 -d > intMsiMockCert.pfx
   ```

1. Transfer certificates to Microsoft Managed Device by using SFTP or SCP so that the certificates can be imported into the keyvault

1. From your MSFT managed device, open the Azure Portal and use PIM (Privileged Identity Management) > My Roles > Azure Resources to activate the `Key Vault Administrator` role in subscription `Azure Red Hat OpenShift v4.x - HCP`.

1. With the azure cli, login to **Azure Red Hat OpenShift v4.x - HCP**

1. Upload certificates to the MSIT INT Key Vault, update `--file` as needed.

   ```bash
   az keyvault certificate import --vault-name arohcpint-svc-ln --name intArmHelperCert --file intArmHelperCert.pfx
   az keyvault certificate import --vault-name arohcpint-svc-ln --name intFirstPartyCert --file intFirstPartyCert.pfx
   az keyvault certificate import --vault-name arohcpint-svc-ln --name intMsiMockCert --file intMsiMockCert.pfx
   ```

1. Deploy cluster-service so that it picks up the new configuration.

1. Test the environment create an HCP and a node pool to validate credentials are setup properly.
