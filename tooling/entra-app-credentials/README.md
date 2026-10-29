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

## Client secrets

`ensure-secret` makes sure applications that authenticate with a client secret
have their credentials in Key Vault. For each `--mapping
'application;<prefix>-client-secret'` it stores three secrets, each only when
missing: `<prefix>-client-secret` (minted with `addPassword`, valid for two
years), `<prefix>-client-id` and `<prefix>-tenant-id`. An existing secret is
never regenerated or overwritten; rotation is a separate, deliberate operation.

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials ensure-secret \
  --vault-url https://example.vault.azure.net \
  --grant-consent \
  --mapping 'application-name;prefix-client-secret'
```

`--grant-consent` reproduces `az ad app permission admin-consent`: each
application permission declared in `requiredResourceAccess` becomes an app role
assignment, and delegated permissions are added to the tenant-wide permission
grant without dropping scopes already granted. It runs on every invocation, is
idempotent, and fails the command if consent cannot be granted. Use `--dry-run`
to see what would change.

The caller needs secret read/write access on the Key Vault and permission to
add credentials to the applications and, with `--grant-consent`, to grant
admin consent in the tenant.
