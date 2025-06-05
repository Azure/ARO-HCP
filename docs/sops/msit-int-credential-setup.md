# MSIT INT Credential Setup

This document provides instructions for setting up **first-party**, **MSI mock**, and **ARM helper** credentials for the MSIT INT environment.

## Overview

This environment is unique because the first-party, MSI mock, and ARM helper credentials exist outside the MSIT subscription. Therefore, some manual steps are required to configure the environment.

1. **Authenticate and set your subscription**

   ```bash
   az account set -n "ARO SRE Team - INT (EA Subscription 3)"
   ```

1. **Create the INT mock identities**
   Execute the `create-int-mock-identities` Make target to create or update the AAD apps and service principals. This generates a certificate in the `aro-hcp-int-kv` Key Vault in the global resource group and refreshes the AAD app credentials with the newly generated certificate.

   ```bash
   cd dev-infrastructure/
   make create-int-mock-identities
   ```

1. **Update configuration**
   If new AAD apps were created, update `config.msft.yaml` with the new values. See [https://github.com/Azure/ARO-HCP/pull/1712](https://github.com/Azure/ARO-HCP/pull/1712) for an example.

1. **Move the certificate bundles to the MSIT INT Key Vault**

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

1. **Log in to the MSIT tenant**
   Use device code login and authenticate via a MSFT managed device.  Choose the `"Azure Red Hat OpenShift v4.x - HCP"` subscription.

   ```bash
   az login --use-device-code
   ```
1. Activate PIM role `Key Vault Administrator` in `"Azure Red Hat OpenShift v4.x - HCP"`
1. **Upload certificates to the MSIT INT Key Vault**

   ```bash
   az keyvault certificate import --vault-name arohcpint-svc-ln --name intArmHelperCert --file intArmHelperCert.pfx
   az keyvault certificate import --vault-name arohcpint-svc-ln --name intFirstPartyCert --file intFirstPartyCert.pfx
   az keyvault certificate import --vault-name arohcpint-svc-ln --name intMsiMockCert --file intMsiMockCert.pfx
   ```

1. **Deploy the cluster service**
   Use the new configuration.

1. **Test the environment**
   Create an HCP and a node pool to validate the setup.
