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

Backend reads the document endpoint and canonical account name through ARM and verifies
that the SQL database name (`properties.resource.id`) exactly equals that name, matching
the infrastructure naming contract. The resolved names are used for data-plane clients
and throughput queries. It reads configured autoscale maximum RU/s (or manual RU/s)
for Resources, Billing and Fleet at startup. Management-cluster container budgets
come directly from Fleet registration's `KubeApplierCosmosContainerName` and
`KubeApplierCosmosContainerMaxScale`, sourced from the same configuration used to
provision those containers. No per-MC throughput ARM requests are made; out-of-band
changes to provisioned throughput are not detected through this metadata.
Successful clients and physical-container budgets are first-success snapshots for
the process lifetime: restart the backend after throughput re-registration. All
controllers sharing a physical container retain that first budget and its debt;
later metadata changes do not resize or reset it. For decreases, coordinate
quiescing backend work through provisioning, registration and restart so an older,
larger snapshot is not used against a smaller ceiling.

The replacement for closed PR #7720 must merge and roll out to production before
PR #7730 may merge or deploy. Follow the [registration rollout prerequisites](../fleet/registration/README.md):
upgrade Fleet and AdminAPI writers, then rerun registration and verify positive
metadata on every existing MC before enabling limited backend traffic. Legacy records may
legally have zero metadata, but limited backend clients reject zero or negative
maximums with an actionable registration error, not as an absent MC. Failed
construction is not cached, so backfill can recover without restart. Startup with
zero MCs succeeds and only queries the three fixed container budgets. Neither
invalid metadata nor discovery errors install an unlimited or static fallback.
Existing utilization, cleanup fractions and explicitly unlimited informer clients
are preserved.

The CLI now requires a real Azure ARM account; a standalone Cosmos emulator cannot
satisfy discovery. Every `StorageFactoryOptions` constructor must supply a
`ContainerThroughput` provider for the three fixed containers, including tests and
direct local workflows. Unit tests inject ARM clients or deterministic throughput
providers; existing integration helpers
construct data-plane clients directly. These workflows remain independent of ARM
without restoring production Helm database-name or endpoint settings.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. Understand that this requires such an environment to be created first via `make personal-dev-env` from the root of the repository.

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository. This way developer images will not conflict with other developer images or CI built ones. The actual deployment is delegated to the pipeline/RP.Backend target in the root of the repository, providing a configuration override for `backend.image.repository` and `backend.image.digest` respectively.

## Deployment

The [pipeline.yaml](pipeline.yaml) file in this directory contains the pipeline definition for the Backend. It is integrated into the [topology.yaml](../topology.yaml) file and runs as part of the service cluster deployment.
