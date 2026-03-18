# Secret sync scripts

see [Secret Syncronization](../../docs/secret-sync.md) for higher level information

## Validating encrypted secrets

The `test-decrypt` make target decrypts and validates all secrets in [`encryptedsecrets.yaml`](../../dev-infrastructure/data/encryptedsecrets.yaml) against the dev Key Vault.

### Prerequisites

- `az login` to the dev tenant (the test uses `DefaultAzureCredential`)
- `yq` installed (used by the Makefile to read rendered config)

### Running the test

```sh
make test-decrypt
```

### What it does

1. Reads encrypted secrets from `encryptedsecrets.yaml`
2. Unwraps each secret's data encryption key using the Key Vault's RSA key (`secretSyncKey`) via RSA-OAEP-256
3. Decrypts the secret data using AES-GCM
4. Validates the decrypted content using secret-specific validators

### Configurable environment variables

| Variable | Default | Description |
|---|---|---|
| `ENCRYPTED_SECRETS_FILE` | `../../dev-infrastructure/data/encryptedsecrets.yaml` | Path to the encrypted secrets file |
| `RENDERED_CONFIG` | `../../config/rendered/dev/dev/westus3.yaml` | Path to rendered config (used to derive Key Vault URI and key name) |
| `KEY_VAULT_URI` | Derived from `RENDERED_CONFIG` | Key Vault URI override |
| `KEY_ENCRYPTION_KEY_NAME` | Derived from `RENDERED_CONFIG` | Encryption key name override |

### Adding new secrets

If a new secret is added to `encryptedsecrets.yaml` without a corresponding validator in [`decrypt_integration_test.go`](decrypt_integration_test.go), the test will **fail**.