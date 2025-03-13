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
├── Makefile
├── pipeline.yaml
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
    ├── Makefile
    ├── pipeline.yaml
    └── deploy
        └── <chart goes here>
```

### Helm chart

Service and infrastructure deployments in MSFTs tenants need to be orchestrated by EV2. EV2 offers a various tooling types for deployment, with the most flexible being the [Shell](pipeline-concept.md#shell-step) extension. The tools that can be used within a Shell extension are well defined and offer `helm`. In addition, EV2 has support for direct Helm deployments in the working, which might be an option in the future.

See the [Shell extension](https://ev2docs.azure.net/features/service-artifacts/actions/shell-extensions/overview.html) documentation for more details about the supported tools that can be used in scripts and Makefiles. Bringing in additional tools into shell steps, like `oc` etc. is not an option for environments with strict security requirements like Fairfax.

#### Namespace management

Helm is not supposed to manage the deployment namespace. This is the responsibility of the [Makefile](#makefile). Helm can rely on the namespace being present and the correct `KUBECONFIG`context being set for deployment.

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

Image references for deployments need to use `sha256` digests instead of tags to enhance security and ensure immutability, e.g. see the configuration structure for `clusterService.image.digest` in [config.yaml](../config/config.yaml).

Service component image need to be provided via the SVC ACR instance (or MSFTs MCR). The respective ACR name can be found in the [config.yaml](../config/config.yaml) under `acr.svc.name`.

It is the responsibility of the Makefile to provide configuration values via `--set key=value` for all required Helm chart configuration options.

#### Helm hooks

The standard Helm chart installation command in Makefiles supports waiting for deployments to complete and for jobs to finish. This way all available [chart hooks](https://helm.sh/docs/topics/charts_hooks/) can be used during deployments.

### Makefile

The `Makefile` in the service directory is the entry point for its deployment operations. It needs to contain a `deploy` target that handles the deployment process. The `deploy` target should be able to take in all required configuration data as environment variables.

```makefile
-include ../setup-env.mk                               (1)
-include ../helm-cmd.mk                                (2)

deploy:                                                (3)
    kubectl create namespace <namespace> --dry-run=client -o json | kubectl apply -f -
    $(HELM_CMD) <deployment-name> ./deploy \           (4)
    --namespace <namespace> \                          (5)
    --set some_key=${SOME_ENV_VAR}                     (6)
```

1. Include the setup-env.mk file to set up some basic environment variables that sets up hooks into [configuration management](configuration.md).
2. Include the helm-cmd.mk file to set up the `HELM_CMD` environment variable. Controls also Helm dry-run mode.
3. Since Helm is not managing the namespace creation, the Makefile should create the namespace if it does not exist.
4. Using the `HELM_CMD` environment variable makes sure a consistent helm command is used across all services.
5. The namespace should be passed to the Helm chart.
6. Configuration values should be passed to the Helm chart using the `--set` flag.

### Deployment via Pipelines

The execution context for any service deployment is a pipeline file, that brings scripts, Makefiles, Helm charts, configuration together and deployment target together.

```yaml
...
resourceGroups:
- name: {{ .svc.rg }}
  subscription: {{ .svc.subscription }}
  aksCluster: {{ .svc.aks.name }}                       (1)
  steps:
  - name: deploy
    action: Shell
    script: make deploy                                 (2)
    variables:                                          (3)
    - name: IMAGE_DIGEST
      configRef: my-service.image.digest
    dryRun:                                             (4)
      variables:
        - name: DRY_RUN
          value: "true"
```

1. defines the AKS cluster that serves as a deployment target
2. executes the `deploy` target in the Makefile of the service directory (paths in commands are relative to the pipeline.yaml location)
3. provides [configuration data](configuration.md) to the Makefile
4. enables dry-run support for the Helm chart
