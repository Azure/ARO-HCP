# ARO HCP Deployment Pipelines

ARO HCP deployment pipelines define the rollout process for infrastructure and services across different [Azure environments](environments.md). These pipelines follow a custom YAML format that allows defining deployment steps targeting specific Azure subscriptions, resource groups, and AKS clusters.

This document is intended for ARO HCP developers and SREs. It provides guidance on writing pipeline files that cover the deployment of different aspects of ARO HCP. Readers will learn how to structure pipeline files, define steps, and combine them effectively to deploy infrastructure and services.

## Pipeline File Structure

ARO HCP pipelines are defined in a custom YAML format that describes the deployment context, specifying which Azure subscriptions and resource groups are targeted. The pipeline file consists of high-level attributes that set the scope for deployments.

The following example illustrates the top-level structure of a pipeline file:

```yaml
serviceGroup: Microsoft.Azure.ARO.HCP.Region     (1)
rolloutName: Region Rollout                      (2)
resourceGroups:                                  (3)
- name: {{ .global.rg }}                         (4)
  subscription: {{ .global.subscription }}       (5)
  aksCluster: {{ .global.aksCluster }}           (6)
  steps: []                                      (7)
```

1. Defines the logical grouping of services being deployed. This value can be freely chosen as long as it is unique and prefixed with `Microsoft.Azure.ARO.HCP`.
2. `rolloutName`: Specifies a human-readable name for the rollout process.
3. `resourceGroups`: A list of deployment contexts targeted by the pipeline.
4. `resourceGroups.name`: The name of the resource group. Can be configured statically or dynamically using [configuration lookups](configuration.md) via Go template syntax.
5. `resourceGroups.subscription`: The Azure subscription ID associated with the resource group. Can be configured statically or dynamically using [configuration lookups](configuration.md) via Go template syntax.
6. `resourceGroups.aksCluster`: Optional. Specifies the name of the AKS cluster that deployment steps should target. Can be configured statically or dynamically using [configuration lookups](configuration.md) via Go template syntax.

## Pipeline Steps

A subscription/resourcegroup/AKS execution context needs to define a sequence of deployment steps. These steps define what is to deployed, how, and in what order.

### Step Structure

A step is defined using a set of properties that determine its function and execution behavior.

```yaml
...
resourceGroups:
- ...
  steps:
  - name: step-name                      (1)
    action: step-type                    (2)
    dependsOn:                           (3)
    - other-step-name
    <step type specific properties>      (4)
```

1. `name`: An identifier for the step that is unique within the entire pipeline file.
2. `action`: The type of step being executed. This can be an ARM step, a shell step, or any other supported step type (see [Step Types](#step-types)).
3. `dependsOn`: An optional list of other step names that must complete successfully before this step is executed. Dependencies must be cycle-free to ensure a valid execution order.
4. Step type specific properties that define the behavior of the step.

### Step Types

ARO HCP pipelines support multiple step types, each designed for a specific deployment scenario. The two most commonly used types are:

#### ARM / Bicep Step

Used for deploying Azure infrastructure using [Bicep](bicep.md) templates. These steps allow defining and updating Azure resources declaratively.

```yaml
  ...
  steps:
  - name: region-infra
    action: ARM                                          (1)
    template: templates/region.bicep                     (2)
    parameters: configurations/region.tmpl.bicepparam    (3)
    deploymentLevel: ResourceGroup                       (4)
```

1. `action: ARM` marks the step as an ARM step.
2. `template`: The path to the Bicep template file that defines the infrastructure to be deployed.
3. `parameters`: The path to the Bicep parameters file that provides input values for the template.
4. `deploymentLevel`: The scope at which the deployment should occur. Valid values are `ResourceGroup` and `Subscription`.

This step type supports dry-run testing via [what-if](https://learn.microsoft.com/en-us/azure/azure-resource-manager/templates/deploy-what-if?tabs=azure-powershell).

Detailed information about Bicep templates in general and how to use the ARM step type, can be found in the [Bicep documentation](bicep.md#deploying-bicep-templates).

#### Shell Step

Execute shell commands or scripts within the pipeline environment. Shell steps are commonly used for deploying services via Helm charts, or performing other automation tasks.

```yaml
  ...
  steps:
  - name: upgrade-istio
    action: Shell                                       (1)
    script: make deploy                                 (2)
    variables:                                          (3)
    - name: TARGET_VERSION                              (4)
      configRef: svc.istio.targetVersion                (5)
    - name: RETRIES
      value: 5                                          (6)
```

1. `action: Shell` marks the step as a shell step.
2. `script`: The shell command to be executed. This can be a single command or a script file.
3. `variables`: A list of environment variables that are set before executing the script.
4. `variables.name`: The name of the environment variable.
5. `variables.configRef`: The [configuration reference](configuration.md) to look up the value for the environment variable.
6. `variables.value`: Alternatively static values can be provided for the environment variable.

Currently, the following list of tools can be used within shell scripts:

- `az`
- `helm`
- `kubectl`
- `jq`
- `make`
- ...

See the [Shell extension](https://ev2docs.azure.net/features/service-artifacts/actions/shell-extensions/overview.html) documentation for more details.

Shell steps also support dry-run testing, but such scripts need to explicitely implement it and mark support for it with the `dryRun` property.

```yaml
  ...
  steps:
  - ...
    action: Shell
    ...
    dryRun:                                          (1)
      variables:
      - name: DRY_RUN                                (2)
        value: "true"
```

1. `dryRun`: Marks the step as supporting dry-run testing.
2. `variables`: A list of environment variables that are set before executing the script.

It is the scripts responsibility to react to the `DRY_RUN` environment variable correctly and not perform any real update actions on the target subscription/resourcegroup/AKS cluster.

>[!WARNING]
> TODO: we need to align and document the tool versions between the EV2 execution context and the Red Hat pipeline runner.

Shell steps are mostly used for service deployments leveraging [Helm charts](service-deployment-concept.md#helm-chart).

### Step execution context

All steps share a common execution context:

#### Azure session

When executing step, an Azure session is provided for the defined subscription (`resourceGroups.subscription`) and resourcegroup (`resourceGroups.name`), allowing authenticated Azure operations. The identity used for this session is provided in the [configuration](configuration.md) as `aroDevopsMsiId`.

> [!IMPORTANT]
> Please note that this identity does not have access to all Azure resources in our subscriptions and resourcegroups by default. The necessary permissions need to be granted explicitly. You can observe this in various Bicep templates, where this identity is granted specific permissions, e.g. on Key Vaults or storage accounts.

#### Azure resource groups

The resourcegroup (`resourceGroups.name`) is pre-created before step execution starts.

#### AKS cluster

If an `resourceGroups.aksCluster` is specified, the `KUBECONFIG` environment variable is set and allows cluster admin interaction withe the AKS cluster. This is mostly relevant for `Shell` steps.

## Pipeline Deployment Scope

A pipeline is the smallest unit of deployment in ARO HCP. This means that all steps within a pipeline are executed from start to finish—there is no concept of executing a single step in only.

A pipeline typically corresponds to one architectural scope, such as global, regional, service or management. More details about these scopes can be found in the [architecture documentation](high-level-architecture.md). However, pipelines are not strictly limited to a single architectural scope. In some cases, cross-scope deployments are necessary, e.g. the regional DNS zone needs delegation from its parent, which resides in the global scope. You can find elaborate details about this in the [Bicep documentation](bicep.md#cross-subscription-deployments).

In most cases, each service has its own dedicated pipeline. This allows services to be rolled out individually.

By structuring pipelines appropriately, deployments can be managed efficiently while ensuring the right level of isolation and access across scopes.

> [!CAUTION]
> A pipelines [ARM step `deploymentLevel`](bicep.md#deploying-bicep-templates) should not be confused with the pipeline deployment scope. While the `deploymentLevel` is a technical aspect that controls if a Bicep module gets applied on a Resource Group vs Subscription level, the deployment scope defined in this section is a conceptual term that describes the alignment of a pipeline with a specific [architectural scope](high-level-architecture.md).

## Pipeline Execution in Different Environments

The various target environments for ARO HCP infrastructure and service deployment have very unique requirements on the rollout process, involving different tools and policies. Microsoft environments require deployments to be driven by ADO and their [EV2](https://ev2docs.azure.net) deployment service, which cannot be used for non-Microsoft environments. The Red Hat developemnt environment on the other hand does not have such strictly regulated rollout processes and we chose to use GitHub actions as the overall driver for rollouts.

The custom pipeline format supports multiple deployment environments and tools to accommodate these different execution requirements and tooling.

> [!TIP]
> You can find out more about the different environments and ARO HCP instances in the [environments documentation](environments.md).

### Microsoft ADO and EV2

For Microsoft tenant deployments, we run ADO pipelines that use a dedicated EV2 generator. This generator translates pipeline files into EV2 manifests, uploads them to EV2, and performs an SDP rollout.

The pipeline format, with its resource group and subscription references and the list of steps, translates very well to the EV2 format. For example, the ARM and Shell step types are built-in step types in EV2. More details about the generation process will be provided in a dedicated document (tbd).

### Red Hat Pipeline Runner

Within the Red Hat development tenant for ARO HCP, we have two primary use cases for deployments.

Developers and SREs deploy their personal development ARO HCP instances using Makefile targets, such as `make infra.all`. Behind the scene, this executes a series of pipelines using a custom pipeline runner that interprets the pipeline files and takes actions accordingly, adhering to the defined expectations described in the [Step execution context](#step-execution-context) section.

In addition, some shared ARO HCP instances are continuously reconciled on each change in the ARO-HCP repository using GitHub Actions. These actions leverage the same Makefile targets and the same pipeline executor as developers and SREs.

The custom pipeline runner can be found in [tooling/templatize](tooling/templatize).

To manually run a pipeline you can use the `templatize.sh` script, e.g. to deploy `my-pipeline.yaml` with the `personal-dev` environment architetype in the `public` cloud, run

```sh
./templatize.sh personal-dev -p my-pipeline.yaml -P run -c public
```

The pipeline runner supports a dry-run mode that allows you to simulate the execution of a pipeline without actually deploying any resources. This is useful for verifying the correctness of the pipeline file and the expected behavior of the steps. Add the `-d` option to the `templatize.sh` command to enable dry-run mode:

```sh
./templatize.sh personal-dev -p my-pipeline.yaml -P run -c public -d
```

> [!IMPORTANT]
Please consult the step specific documentation for more details on the behavior of each step type during dry-run.
