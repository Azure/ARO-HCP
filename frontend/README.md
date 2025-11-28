# ARO-HCP-FRONTEND

## Development Workflow

The Frontend can be built and tested locally and in personal DEV environments using a set of Makefile targets.

- **make run:** runs the Frontend binary locally
- **make deploy:** builds the Frontend container image, uploads it to the DEV service ACR and deploys it to a personal DEV cluster

The `Makefile` has access to a set of environment variables representing configuration from the `config/config.yaml` file. The environment variables are made available via the `include ../setup-templatize-env.mk` directive in the `Makefile`, which processes and includes the [Env.mk](Env.mk) file. This is the file you need to modify to provide additional environment variables fueled by `config.yaml`.

### Local Run

Using the `make run` target, the Frontend binary can be run locally.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. Understand that this requires such an environment to be created first via `make personal-dev-env` from the root of the repository.

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository. This way developer images will not conflict with other develooper images or CI built ones. The actual deployment is delegated to the pipeline/AdminAPI target in the root of the repository, providing a configuration override for `frontend.image.repository` and `frontend.image.digest` respectively.

## Deployment

The [pipeline.yaml](pipeline.yaml) file in this directory contains the pipeline definition for the Frontend. It is integrated into the [topology.yaml](../topology.yaml) file and runs as part of the service cluster deployment.

## Available endpoints

> Note: If you need a test cluster.json file or node_pool.json for some of the below API calls, you can generate one using [utils/create.go](./utils/create.go)
> `go run utils/create.go -type cluster`
> or
> `go run utils/create.go -type node_pool`
> Any Create/Get/Delete cluster calls below will expect a running CS in order to function for now



Update a subscription state (Must be **Registered** for other calls to function)
```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000?api-version=2.0" --json '{"state":"Registered", "registrationDate": "now", "properties": { "tenantId": "00000000-0000-0000-0000-000000000000"}}'
```

List the Operations for the Provider
```bash
curl -X GET "localhost:8443/providers/Microsoft.RedHatOpenShift/operations?api-version=2024-06-10-preview"
```

List HcpOpenShiftVersions Resources by Location

```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOpenShiftVersions?api-version=2024-06-10-preview"
```

Get HcpOpenshiftVersion Resource

```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOpenShiftVersions/4.19.7?api-version=2024-06-10-preview"
```

List HcpOpenShiftClusterResource Resources by Subscription ID
```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters?api-version=2024-06-10-preview"
```

Get a HcpOpenShiftClusterResource
```bash
curl -X GET "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster?api-version=2024-06-10-preview"
```

Create or Update a HcpOpenShiftClusterResource

```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster?api-version=2024-06-10-preview" \
  -H "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"aro-hcp-local-testing\", \"createdByType\": \"User\", \"createdAt\": \"2024-06-06T19:26:56+00:00\"}" \
  -H "X-Ms-Identity-Url": https://dummyhost.identity.azure.net" \
  --json @cluster.json
```

You will notice that the request contains a `X-Ms-Identity-Url` with the value `https://dummyhost.identity.azure.net`. Setting the `X-Ms-Identity-Url` HTTP header when interacting directly
with the Frontend is required. However, for the environments where a real managed identities data plane does not exist the value can be any arbitrary/dummy HTTPS URL that ends in `identity.azure.net`.

Delete a HcpOpenShiftClusterResource
```bash
curl -X DELETE "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster?api-version=2024-06-10-preview"
```

Execute deployment preflight checks
```bash
curl -X POST "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/deployments/YOUR_DEPLOYMENT_NAME/preflight?api-version=2020-06-01" --json preflight.json
```

Node pool operations:

Create node pool
```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/nodePools/dev-nodepool?api-version=2024-06-10-preview" \
  -H "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"aro-hcp-local-testing\", \"createdByType\": \"User\", \"createdAt\": \"2024-06-06T19:26:56+00:00\"}" --json @node_pool.json
```

Get node pool
```bash
curl "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/nodePools/dev-nodepool?api-version=2024-06-10-preview"
```

Delete node pool
```bash
curl -X DELETE "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/nodePools/dev-nodepool?api-version=2024-06-10-preview"
```

External Auth operations:

Create external auth
```bash
curl -X PUT "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/externalAuths/entra?api-version=2024-06-10-preview" \
  -H "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"aro-hcp-local-testing\", \"createdByType\": \"User\", \"createdAt\": \"2024-06-06T19:26:56+00:00\"}" --json @external_auth.json
```

Get external auth
```bash
curl "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/externalAuths/entra?api-version=2024-06-10-preview"
```

Delete external auth
```bash
curl -X DELETE "localhost:8443/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dev-test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/dev-test-cluster/externalAuths/entra?api-version=2024-06-10-preview"
```
