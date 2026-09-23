# Entra application credentials

`entra-app-credentials` manages the self-signed Key Vault certificates used by
Microsoft Entra applications and reconciles their pinned credentials through
Microsoft's official Graph SDK.

The `pin` command intentionally replaces an application's complete
`keyCredentials` collection with the requested certificate. Use it only for
applications whose certificate credentials are exclusively managed by this
command. Use `--dry-run` to compare credentials without changing the application.

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials pin \
  --vault-url https://example.vault.azure.net \
  --mapping 'application-name;certificate-name;certificate.example.com' \
  --indexed-application-base application-pool \
  --indexed-certificate-base certificate-pool \
  --indexed-certificate-dns-suffix pool.example.com \
  --indexed-count 20 \
  --create-missing
```

`--create-missing` uses the established mock-certificate policy and never
changes an existing certificate or policy. Without it, the Key Vault certificate
must already exist; `pin` only reconciles that certificate onto the application.

Rotation is intentionally disruptive and excluded from deployment pipelines.
It replaces the current Key Vault certificate and then pins the new thumbprint.
The `--rotate` flag explicitly requests this lifecycle operation:

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials pin \
  --vault-url https://example.vault.azure.net \
  --mapping 'application-name;certificate-name;certificate.example.com' \
  --rotate
```

Authentication may fail between certificate replacement and successful Graph
reconciliation. If pinning fails, rerun the normal privileged pipeline to pin
the current Key Vault certificate; do not immediately request another rotation.

The command uses `DefaultAzureCredential` with token-based credentials only, so
`AZURE_TOKEN_CREDENTIALS` must be set for the execution environment. The caller
needs certificate-read access on the Key Vault, certificate-create access when
using lifecycle flags, and permission to update the target Entra applications.
It retries Microsoft Graph propagation and throttling failures, skips applications
that already contain exactly the requested pinned certificate, and verifies each
successful update by thumbprint.
