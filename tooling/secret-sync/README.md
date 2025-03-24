# Secret sync scripts

see [Secret Syncronization](../../docs/secret-sync.md) for higher level information

## encryption/decryption


This tool is meant to encrypt data using RSA and the provided RSA key in this repo. It is meant to decrypt data using the keyvault key decrypt api and store the decrypted data in the same keyvault.

This tool acts on a single key/secret use scripts to loop over more than one secret/key.

To encrypt a file using a specific key run:
```
echo "datasdmkiopjkoisdjfoisdjfiosdfa" | PUBLIC_KEY_FILE=publickey.pem \
OUTPUT_FILE=test.enc \
go run main.go encrypt
```

To decrypt a file run:
```
INPUT_FILE=test.enc \
ENCRYPTION_KEY=testing \
SECRET_TO_SET=jboll-testing \
VAULT_NAME=testingjboll \
go run main.go decrypt
```

## encrypt-all.sh

Script that iterates over all keys in `data/keys` and encrypts the provided data.

## decrypt-all.sh

Script that iterates over a folder in `data/encryptedsecrets` and decrypts it.
