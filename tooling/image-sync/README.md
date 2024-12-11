# image-sync

This utility is used for syncing images from various sources to ACR. It is used, i.e. to copy over images from the Quay to the Azure Container Registry.

## Configuration

The main configuration looks like this:

```YAML
repositories:
    - registry.k8s.io/external-dns/external-dns
numberOfTags: 3
acrTargetRegistry: someregistry.azurecr.io
tenantId: 1ab61791-4b66-4ea4-85ff-aa2c0bf37e57
secrets:
  - registry: registry.k8s.io
    secretFile: /secret.txt
```

Explanation:
- `repositories` - list of repositories to sync. Do not specify tags, since this utility will sync only the latest tags.
- `numberOfTags` - number of tags to sync. The utility will sync the latest `numberOfTags` tags.
- `quaySecretfile` - path to the secret file for the Quay registry.
- `acrTargetRegistry` - the target registry.
- `tenantId` - the tenant ID used for authentication with Azure.
- `RequestTimeout` - the timeout for the HTTP requests. Default is 10 seconds.
- `secrets` - Array of secrets used for API authentitcation


### quaySecretfile

The secret file for the Quay registry should look like this:
```JSON
{
  "BearerToken": "ibiw0990J09jw90fjwelakjsda1kl2KJdndfssd", # notsecret
}
```

### Pull Secrets

Authentication leverages the standard containers auth files. It is described here: [https://github.com/containers/image/blob/main/docs/containers-auth.json.5.md#description](https://github.com/containers/image/blob/main/docs/containers-auth.json.5.md#description)

Thus, create an authorization file. You can override the path using `XDG_RUNTIME_DIR`: `${XDG_RUNTIME_DIR}/containers/auth.json`.
