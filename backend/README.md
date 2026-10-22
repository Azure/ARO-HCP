# ARO-HCP-BACKEND

## Development Workflow

The Backend can be built and tested locally and in personal DEV environments using a set of Makefile targets.

- **make run:** runs the Backend binary locally
- **make deploy:** builds the Backend container image, uploads it to the DEV service ACR and deploys it to a personal DEV cluster

The `Makefile` has access to a set of environment variables representing configuration from the `config/config.yaml` file. The environment variables are made available via the `include ../setup-templatize-env.mk` directive in the `Makefile`, which processes and includes the [Env.mk](Env.mk) file. This is the file you need to modify to provide additional environment variables fueled by `config.yaml`.

### Local Run

Using the `make run` target, the Backend binary can be run locally.

Backend accepts `--cosmos-account-resource-id` (or `COSMOS_ACCOUNT_RESOURCE_ID`),
replacing the database name and URL inputs. `Env.mk` resolves the account ID for
personal DEV environments; it can also be supplied explicitly. The active backend
credential must have the built-in Reader role on that Cosmos account, in addition
to its existing Cosmos data-plane permissions. Deployment templates assign Reader
to the backend managed identity at account scope, not resource-group or subscription
scope. Reader grants `*/read`; it is not a guarantee against all key retrieval
because the provider exposes `databaseAccounts/readonlykeys/read`. Account local
authentication remains disabled by default.

Backend reads the document endpoint through ARM and verifies that the SQL database
name equals the account name, matching the infrastructure naming contract. It reads
configured autoscale maximum RU/s (or manual RU/s) for Resources, Billing and Fleet
at startup, and independently for each physical management-cluster container on
first use. Successful discoveries are cached for the process lifetime; restart the
backend to adopt subsequent throughput changes. Discovery failures do not install
an unlimited fallback, and failed MC lookups can be retried. Existing utilization,
cleanup fractions and explicitly unlimited informer clients are preserved.

The CLI now requires a real Azure ARM account; a standalone Cosmos emulator cannot
satisfy discovery. Unit tests inject ARM clients or `StorageFactoryOptions` throughput
values/providers, and existing integration helpers construct data-plane clients
directly. These workflows remain independent of ARM without restoring production
Helm database-name or endpoint settings.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. Understand that this requires such an environment to be created first via `make personal-dev-env` from the root of the repository.

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository. This way developer images will not conflict with other developer images or CI built ones. The actual deployment is delegated to the pipeline/RP.Backend target in the root of the repository, providing a configuration override for `backend.image.repository` and `backend.image.digest` respectively.

## Deployment

The [pipeline.yaml](pipeline.yaml) file in this directory contains the pipeline definition for the Backend. It is integrated into the [topology.yaml](../topology.yaml) file and runs as part of the service cluster deployment.
