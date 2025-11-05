# Investigating Infrastructure Rollout Timings

When starting a personal environment using our entrypoint execution tools, a `timing.yaml` will be placed on the local filesystem with details of the steps taken and their runtime:

```shell
$ AZURE_CONFIG_DIR=~/.azure-redhat make entrypoint/Region
tooling/templatize/templatize entrypoint run --config-file "config/config.yaml" \
							     --config-file-override "" \
                                 --topology-config topology.yaml \
                                 --dev-settings-file tooling/templatize/settings.yaml \
                                 --dev-environment pers \
                                 --entrypoint Microsoft.Azure.ARO.HCP.Region \
                                 --dry-run="false" \
                                 --verbosity=3 \
                                 --timing-output=timing/steps.yaml
$ # ...
$ head -n 18 timing/steps.yaml
- identifier:
    resourceGroup: management
    serviceGroup: Microsoft.Azure.ARO.HCP.Management.Infra
    step: infra-output
  info:
    finishedAt: "2025-11-05T06:04:27-07:00"
    queuedAt: "2025-11-05T06:03:53-07:00"
    startedAt: "2025-11-05T06:03:53-07:00"
    state: succeeded
- identifier:
    resourceGroup: management
    serviceGroup: Microsoft.Azure.ARO.HCP.Management.Infra
    step: storageclass
  info:
    finishedAt: "2025-11-05T06:16:53-07:00"
    queuedAt: "2025-11-05T06:16:35-07:00"
    startedAt: "2025-11-05T06:16:35-07:00"
    state: succeeded
```

Use the graph visualizer to determine the dependency links between steps in the graph:

```shell
$ make graph/entrypoint/Region
tooling/templatize/templatize entrypoint graph --config-file "config/config.yaml" \
                               --topology-config topology.yaml \
                               --dev-settings-file tooling/templatize/settings.yaml \
                               --dev-environment pers \
                               --entrypoint Microsoft.Azure.ARO.HCP.Region > .graph.dot
$ dot -T svg .graph.dot -o .graph.svg
$ # now open .graph.svg
```

Use the HTML visualizer to create an interactive web view for the timing of steps, which will not explicitly detail the dependencies between steps, but will show their runtime relative to each other. Combining this with the graph from above should give insights on what step or steps are blocking others. Similarly, the HTML visualization for ARM deployment operations will show the timeline, but not necessarily the dependencies. Remember that `.bicep` modules run their own deployments, and ARM will parallelize resources inside a deployment, but not across deployments.

```shell
$ make visualize
tooling/templatize/templatize entrypoint visualize --timing-input timing/steps.yaml --output timing/
$ tree timing/
timing/
├── Microsoft.Azure.ARO.HCP.AdminAPI
│ └── service
│     └── output
│         └── arm.html
├── Microsoft.Azure.ARO.HCP.Management.Infra
│ ├── management
│ │ ├── arobit-output
│ │ │ └── arm.html
│ │ ├── cluster
│ │ │ └── arm.html
│ │ ├── cluster-output
│ │ │ └── arm.html
│ │ ├── cs-akv-permissions
│ │ │ └── arm.html
│ │ ├── infra
│ │ │ └── arm.html
│ │ ├── infra-output
│ │ │ └── arm.html
│ │ └── nsp
│ │     └── arm.html
│ └── service
│     └── output
│         └── arm.html # this is the view of ARM operations for a given deployment
├── steps.html # this is the overall timing view of pipeline steps
└── steps.yaml

```