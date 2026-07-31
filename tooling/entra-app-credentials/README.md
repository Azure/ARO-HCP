# Entra application credentials

`entra-app-credentials` reconciles certificate credentials on Microsoft Entra
applications. It reads public certificates directly from the Azure Key Vault
data plane and updates the applications through Microsoft's official Graph SDK.

The `pin` command requires `--replace-all` because it intentionally replaces an
application's complete `keyCredentials` collection. Use it only for applications
whose certificate credentials are exclusively managed by this command.

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials pin \
  --vault-url https://example.vault.azure.net \
  --mapping application-name=certificate-name \
  --indexed-application-base application-pool \
  --indexed-certificate-base certificate-pool \
  --indexed-count 20 \
  --replace-all
```

The command uses `DefaultAzureCredential` with token-based credentials only, so
`AZURE_TOKEN_CREDENTIALS` must be set for the execution environment. The caller
needs certificate-read access on the Key Vault and permission to update the
target Entra applications.
It retries Microsoft Graph propagation and throttling failures, skips applications
that already contain exactly the requested pinned certificate, and verifies each
successful update by thumbprint.
