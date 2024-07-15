# image-sync

This utility is used for syncing images from various sources to ACR. It is used, i.e. to copy over images from the Quay to the Azure Container Registry.

## Configuration

The main configuration looks like this:

```YAML
repositories:
    - registry.k8s.io/external-dns/external-dns
numberOfTags: 3
quaySecretfile: /var/run/quay-secret.json
acrRegistry: someregistry.azurecr.io
tenantId: 1ab61791-4b66-4ea4-85ff-aa2c0bf37e57
```

Explanation:
- `repositories` - list of repositories to sync. Do not specify tags, since this utility will sync only the latest tags.
- `numberOfTags` - number of tags to sync. The utility will sync the latest `numberOfTags` tags.
- `quaySecretfile` - path to the secret file for the Quay registry.
- `acrRegistry` - the target registry.
- `tenantId` - the tenant ID used for authentication with Azure.
- `RequestTimeout` - the timeout for the HTTP requests. Default is 10 seconds.


### quaySecretfile

The secret file for the Quay registry should look like this:
```JSON
{
  "BearerToken": "ibiw0990J09jw90fjwelakjsda1kl2KJdndfssd", # notsecret
  "PullUsername": "quay+user", # notsecret
  "PullPassword": "wzo3PqL3eTdqv42AbsBiFNdhGh6u" # notsecret
}
```

