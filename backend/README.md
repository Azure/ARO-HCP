# ARO-HCP-BACKEND

## Development Workflow

The Backend can be built and tested locally and in personal DEV environments using a set of Makefile targets.

- **make run:** runs the Backend binary locally
- **make deploy:** builds the Backend container image, uploads it to the DEV service ACR and deploys it to a personal DEV cluster

The `Makefile` has access to a set of environment variables representing configuration from the `config/config.yaml` file. The environment variables are made available via the `include ../setup-templatize-env.mk` directive in the `Makefile`, which processes and includes the [Env.mk](Env.mk) file. This is the file you need to modify to provide additional environment variables fueled by `config.yaml`.

### Local Run

Using the `make run` target, the Backend binary can be run locally.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. Understand that this requires such an environment to be created first via `make personal-dev-env` from the root of the repository.

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository. This way developer images will not conflict with other developer images or CI built ones. The actual deployment is delegated to the pipeline/RP.Backend target in the root of the repository, providing a configuration override for `backend.image.repository` and `backend.image.digest` respectively.

## Deployment

The [pipeline.yaml](pipeline.yaml) file in this directory contains the pipeline definition for the Backend. It is integrated into the [topology.yaml](../topology.yaml) file and runs as part of the service cluster deployment.

### MSI mock certificate rotation

When the insecure managed-identity mock is enabled, the backend polls the configured certificate bundle every minute. Retained SMI Azure SDK clients and MI-dataplane access-token retrievers share the current credential generation, so CSI certificate rotation does not require a backend restart. Already-issued access tokens remain usable until the SDK refreshes them.

The initial bundle must be readable and parseable. An invalid replacement is logged and the last successfully loaded credential remains available; a later corrected bundle is loaded on the next change check. Client ID, principal ID and tenant ID remain startup configuration and require a rollout when changed. Real managed-identity dataplane and workload-identity behavior are unchanged.
