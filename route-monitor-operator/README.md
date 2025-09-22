# Route Monitor Operator

This directory contains the deployment configuration and tooling for the Route Monitor Operator (RMO) in ARO-HCP.

## Overview

The Route Monitor Operator is deployed using Helm charts that are generated from upstream OLM bundles. The deployment uses a two-step process:
1. Generate Helm charts from OLM bundles
2. Deploy using the Helm pipeline step

## Upgrade Process

To upgrade the Route Monitor Operator to a new version, follow these steps:

### 1. Update Bundle Version

Edit the `RMO_BUNDLE_VERSION` variable in the `Makefile`:

```makefile
RMO_BUNDLE_VERSION ?= <new-version>
```

Example:
```makefile
RMO_BUNDLE_VERSION ?= 0.1.750-gabcd123
```

### 2. Regenerate Helm Chart

Generate the new Helm chart from the updated bundle:

```bash
make helm-chart
```

This will:
- Clone the upstream bundle repository
- Extract the specified bundle version
- Convert OLM bundle to Helm chart format
- Apply necessary patches and formatting

### 3. Update Configuration

Update the image digests in the main configuration file:

```bash
make update-digests-in-config
```

This will:
- Read the image digests from the generated Helm chart values
- Update `config/config.yaml` with the new digest values
- Preserve all other image properties (registry, repository)

## Files Structure

```
route-monitor-operator/
├── Makefile                    # Build and deployment targets
├── README.md                   # This file
├── pipeline.yaml              # Deployment pipeline definition
├── olm-bundle-repkg-config.yaml # OLM to Helm conversion config
├── scaffold/                  # Helm chart scaffolding files
└── deploy/
    └── helm/
        ├── values.yaml                 # Templated values for pipeline (hand-maintained)
        └── route-monitor-operator/     # Generated Helm chart (auto-generated)
            ├── values-generated.yaml   # Generated values from OLM bundle (auto-generated)
            ├── templates/              # Generated Helm templates (auto-generated)
            └── Chart.yaml             # Generated chart metadata (auto-generated)
```

## Available Make Targets

- `make helm-chart` - Generate Helm chart from OLM bundle
- `make update-digests-in-config` - Update image digests in config.yaml
- `make deploy` - Deploy using Helm (requires ACR and image variables)

## Configuration

The operator configuration is managed in:
- **Bundle Version**: `Makefile` (`RMO_BUNDLE_VERSION` variable)
- **Image Digests**: `config/config.yaml` (under `defaults.routeMonitorOperator`)
- **Deployment Settings**: `pipeline.yaml` (Helm step configuration)

### Values Files Approach

This deployment uses a two-values-file approach to handle both generated and templated values:

- **`deploy/helm/values.yaml`**: Hand-maintained file with pipeline templating (e.g., `{{ .registry }}`)
  - Used by the Helm pipeline step for actual deployment
  - Contains templated references that get resolved during pipeline execution
  - Located outside the auto-generated chart directory to prevent deletion during chart regeneration

- **`deploy/helm/route-monitor-operator/values-generated.yaml`**: Auto-generated from OLM bundle
  - Contains actual values extracted from the upstream OLM bundle
  - Used by the `update-digests-in-config` target to sync digests to config.yaml
  - Gets regenerated every time `make helm-chart` runs

## Notes

- The `deploy/helm/route-monitor-operator/` directory is auto-generated and should not be edited manually
- The `deploy/helm/values.yaml` file is hand-maintained and located outside the auto-generated chart directory
- Image digests are automatically extracted from the OLM bundle during chart generation
- The upgrade process ensures consistency between the bundle version and image references
