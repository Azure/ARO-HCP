# Pipeline Topology

## Overview

The [pipeline topology](../topology.yaml) is a file that describes the dependencies between pipelines and their respective scope in the ARO HCP platform. It establishes a top-down tree where each child pipeline depends on its parent pipeline to prepare the infrastructure and services required for it to succeed. This hierarchical model helps coordinate complex region buildouts.

The topology is maintained collaboratively by all ARO HCP teams, with each team responsible for adding or updating pipeline entries relevant to their components.

## Concepts

### Top-Down Execution Model

The topology defines a strict tree that reflects both pipeline execution order and the [architectural scopes](high-level-architecture.md) of ARO HCP. For example, global infrastructure appears at the top of the tree, followed by regional scope, then service and management infrastructure, and finally the pipelines that deploy specific components. Each pipeline must be placed under the pipeline that prepares their required infrastructure or services.

Currently, there is no automated mechanism that enforces pipeline execution order based on this topology. It is the responsibility of engineers to execute the pipelines in the correct sequence. Automation of this process is expected in the future.

### Services and Service Groups

Pipelines relate to a service group, which is a unique identifier required in the EV2 deployment system. It serves as a namespace to group resources created by a pipeline. It maps directly to one `pipeline.yaml` file and is used to group service artifacts and rollouts within the [EV2 portal](https://ra.ev2portal.azure.net/). In ARO HCP, all service group identifiers must begin with `Microsoft.Azure.ARO.HCP.`.

Service groups also carry semantic meaning within EV2. They define ownership boundaries and are linked to service tree entries, making it clear who maintains each portion of the system. Ownership and service tree metadata is not maintained in the topology file but is managed by the processes defined in the [sdp-pipelines](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines) tooling.

### Entrypoints

Core idea: provide a way to trigger a pipeline and all its child pipelines, allowing for a single entry point to deploy a service and its dependencies. An entrypoint is an approved starting point for pipeline traversal.

> [!NOTE]
> Entrypoints are not currently used in practice but are included for future extensibility. Ignore them while managing the topology file.

## How to Use and Extend the Topology

New `pipeline.yaml` file references need to be added to the `topology.yaml` file to make them deployable via ADO and EV2. A new entry within `services` must be created, following the guidelines below.

### Placement Guidelines

To determine the correct location for a new pipeline:

* If it deploys components on a **service cluster**, place it under `Microsoft.Azure.ARO.HCP.Service.Infra`
* If it deploys components on a **management cluster**, place it under `Microsoft.Azure.ARO.HCP.Management.Infra`
* For pipelines that fall outside these two scopes, consult with the ARO HCP service lifecycle team and Microsoft to determine the appropriate placement.

This ensures pipelines are positioned relative to their infrastructure dependencies.

The new entry must be placed:

* as a `children` entry under an existing service group (most common), or
* at the top level under `services` (rare, only for new root pipelines)

### Adding a New Pipeline

Each new pipeline must be added with a dedicated `serviceGroup`. To define a new `serviceGroup`, follow this pattern:

```yaml
serviceGroup: Microsoft.Azure.ARO.HCP.<uniqueSuffix>
pipelinePath: relative/path/to/pipeline.yaml
purpose: Short description of the pipeline's role
```

Use a meaningful, unique suffix to extend the required prefix `Microsoft.Azure.ARO.HCP.`

### Review Process

All changes to `topology.yaml` are subject to service lifecycle team review. This includes both the addition of new entries and structural changes. The resulting changes to pipeline definitions in the `sdp-pipelines` repository will also go through review.

### Placement Intent

Pipelines should be placed according to **execution dependency**, not just ownership or team alignment. A child pipeline must rely on its parent to set up necessary infrastructure or prerequisites before it runs.

## Tooling and Automation

When the topology file changes, two processes need to be triggered (currently manually) to ensure the new data is reflected in the ARO HCP deployment system:

* **Documentation Generator**: The ARO-HCP repository includes a tool at `tooling/pipeline-documentation` which renders the topology into human-readable form at [/docs/pipeline.md](../docs/pipelines.md). This serves as a reference for engineering and service lifecycle teams.

  To generate the documentation, run `make -C docs pipelines.md`

* **Pipeline Generator (sdp-pipelines)**: The ADO repository [sdp-pipelines](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines) imports the `topology.yaml` from the ARO-HCP repo to generate and register all defined pipelines.

  In order to manifest the changes of `topology.yaml` in actual ADO pipelines, follow the guide in the [sdp-pipelines HCP README](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/README.md).
