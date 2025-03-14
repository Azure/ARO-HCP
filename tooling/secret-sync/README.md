# Secret sync scripts

see [Secret Syncronization](../../docs/secret-sync.md) for higher level information

## encrypt.sh

Used to encrypt secrets with a single output:

`echo content | encrypt.sh rsa-public.pem outputFile`

To encrypt a secret with all available keys, run:
`echo content | encrypt.sh outputFile`
This will encrypt the secret with all available keys. The `outputFile` should be a base name, it will store the secret using this name in each folder for all key vaults under `data/encryptedsecrets`

Optional: set `$DATADIRPREFIX`, path to read/store data from/to defaults to: dev-infrastructure/data

## decrypt.sh

Use this script to decrypt a secret using a key stored in Key Vault:
`decrypt.sh file outputSecret key-vault privateKeySecret`

- file: encrypted file
- outputSecret: secret to write decrypted secret to
- key-vault: keyvault containing the key and store decrypted secret to
- privateKeySecret: keyname used to decrypt 

## decrypt-all.sh

Script that iterates over a folder in `data/encryptedsecrets` and used `decrypt.sh` to decrypt it.
