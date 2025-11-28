# Service Deployment Concept

This chapter outlines

- the proposed structure for a service directory
- how to use Helm charts and pipelines together
- how to inject configuration into Helm charts
- how to test Helm charts with pipeline dry-runs

## Service artifacts

All artifacts for a service deployment live in their own top-level directory in the ARO HCP repository and adheres roughly to the following structure:

```plaintext
my-service
├── Env.mk
├── Makefile
├── pipeline.yaml
├── values.yaml
├── some-script.sh
└── deploy
    └── <chart goes here>
```

If a service consists of multiple individual sub-services, each of them can have their own directory structure as shown above underneath their parent directory.

```plaintext
my-service
├── sub-service-1
...
└── sub-service-2
    ├── Env.mk
    ├── Makefile
    ├── pipeline.yaml
    ├── values.yaml
    └── deploy
        └── <chart goes here>
```

### Helm chart

Service and infrastructure deployments in MSFTs tenants need to be orchestrated by EV2. EV2 offers a various tooling types for deployment, with the most flexible being the [Shell](pipeline-concept.md#shell-step) extension. The tools that can be used within a Shell extension are well defined and offer `helm`. In addition, EV2 has support for direct Helm deployments in the working, which might be an option in the future.

See the [Shell extension](https://ev2docs.azure.net/features/service-artifacts/actions/shell-extensions/overview.html) documentation for more details about the supported tools that can be used in scripts and Makefiles. Bringing in additional tools into shell steps, like `oc` etc. is not an option for environments with strict security requirements like Fairfax.

#### Namespace management

Upstream Helm is not supposed to manage the deployment namespace, but our Helm step handles this concern for us.

Namespaced manifest should declare their namespace in the manifest itself.

```yaml
kind: ...
apiVersion: ...
metadata:
  ...
  namespace: {{ .Release.Namespace }}
```

#### Configuration

Helm charts need to provide enough configuration options via `values.yaml`. It is the responsibility of the Makefile to provide the correct values for the deployment from either the [configuration management](configuration.md) or from looking up dynamic configuration from the deployment context during deployment time.

Image references for deployments need to use `sha256` digests instead of tags to enhance security and ensure immutability, e.g. see the configuration structure for `clustersService.image.digest` in [config.yaml](../config/config.yaml).

Service component image need to be provided via the SVC ACR instance (or MSFTs MCR). The respective ACR name can be found in the [config.yaml](../config/config.yaml) under `acr.svc.name`.

It is the responsibility of the values file template to link to configuration values via `{{ .template.fields }}` for all required Helm chart configuration options.

#### Helm hooks

The standard Helm installation step in tooling supports waiting for deployments to complete and for jobs to finish. This way all available [chart hooks](https://helm.sh/docs/topics/charts_hooks/) can be used during deployments.

### Makefile

Components can be built and tested locally and in personal DEV environments using a set of Makefile targets.

- **make run:** runs the component binary locally
- **make deploy:** builds the component container image, uploads it to the DEV service ACR and deploys it to a personal DEV cluster

The `Makefile` has access to a set of environment variables representing configuration from the `config/config.yaml` file. The environment variables are made available via the `include ../setup-templatize-env.mk` directive in the `Makefile`, which processes and includes the Env.mk file. This is the file you need to modify to provide additional environment variables fueled by `config.yaml`.

### Local Run

Using the `make run` target, the Frontend binary can be run locally.

### Personal DEV Environment deployment

The local code can also be deployed directly into a personal DEV environment by running `make deploy`. Understand that this requires such an environment to be created first via `make persional-dev-env` from the root of the repository.

`make deploy` builds a custom developer image from the local code and uploads it to the DEV service ACR (`arohcpsvcdev`) into a developer specific repository. This way developer images will not conflict with other developer images or CI built ones. The actual deployment is delegated to the pipeline/<service-group-suffix> target in the root of the repository, providing a configuration override for `<component>.image.repository` and `<component>.image.digest` respectively.

## Deployment

The pipeline.yaml file in the directory contains the pipeline definition for component. It is integrated into the [topology.yaml](../topology.yaml) file and runs as part of the service deployment.

The `Makefile` in the service directory is the entry point for its local deployment operations. It needs to contain a `deploy` target that handles the deployment process. The `deploy` target should be able to take in all required configuration data as environment variables.
