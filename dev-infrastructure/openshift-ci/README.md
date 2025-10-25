# OpenShift Release Bot MSFT Test

Since the MSFT Test Test tenant might go away at some point, we need to be prepared to recreate all the SPs and RBAC required for the OpenShift Release Bot so our E2E tests can continue to run.

## Prerequisites

- Enable Global Administrator via PIM (for app registration API Resource permission admin consent)
- Enable User Access Administrator / Owner role via PIM (for role assignments to the app registration)
- az cli installed
- Hashicorpvault cli installed
- az login --tenant 93b21e64-4824-439a-b893-46c9b2a51082 --use-device-code

## Create the application registration

Run `./create-openshift-release-bot-msft-test.sh` to

- create the app registration named `OpenShift Release Bot MSFT Test`
- grant roles
- grant API permissions
- grant admin consent
- upload credentials to Openshift CI Vault

## Recycle credentials

In case you need to recycle credentials, run the following command to:

- create new client secret
- upload credentials to Openshift CI Vault

```bash
./recycle-openshift-release-bot-creds.sh \
    --app "OpenShift Release Bot MSFT Test" \
    --vault-url "https://vault.ci.openshift.org:8200" \
    --vault-secret "selfservice/hcm-aro/hcp-msft-test-credentials" \
    --target-name "hcp-msft-test-test-credentials"
```

If you provide `--delete-old` flag, the old credentials will be deleted from the app registration.
