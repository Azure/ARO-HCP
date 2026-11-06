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
never regenerated or overwritten unless `--rotate` is given.

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
idempotent, and only warns if consent cannot be granted (it usually needs a
tenant admin), so the secrets are still reconciled. Use `--dry-run`
to see what would change.

### Expiry and rotation

Every run checks the stored client secret. Graph never returns secret values,
only the first three characters (the `hint`), so the tool finds the application
credential the stored secret was minted from by matching that hint and reads its
end date:

- expired, or matching no credential of the application (deleted or replaced
  outside Key Vault): the run **fails**, with or without `--dry-run`. The other
  secrets and consent of that application are still reconciled first, and the
  error prints the `--rotate` command for the mapping;
- expiring within `--expiry-warning` (default 90 days, `0` to only report
  expired secrets): a `WARNING` is logged and the run succeeds.

`--rotate` mints a new client secret for every `--mapping` and stores it as a
new Key Vault secret version, even if a valid one exists. The client ID, tenant
ID and the application's existing credentials are left alone, so the old secret
keeps working until it expires or is deleted. `--dry-run --rotate` changes
nothing and never fails on expiry: it reports the current expiry (warning if
expired or expiring) and that it would rotate, which is the way to check a
principal without knowing whether it needs rotation.

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials ensure-secret \
  --vault-url https://example.vault.azure.net \
  --mapping 'application-name;prefix-client-secret' \
  --rotate
```

`pin` applies the same policy to Key Vault certificates, using the expiry Key
Vault reports: an expired certificate fails the run, one expiring within
`--expiry-warning` warns, and `--dry-run --rotate` only reports. `--rotate`
without `--dry-run` replaces the certificate, so no expiry check applies.

The caller needs secret read/write access on the Key Vault and permission to
read and add credentials to the applications and, with `--grant-consent`, to grant
admin consent in the tenant.
