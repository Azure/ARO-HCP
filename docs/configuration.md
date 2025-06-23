# Configuration Management

Managing configuration effectively is crucial for ensuring that deployments remain consistent and adaptable to various environments. Configuration data for every aspect of ARO HCP is stored in a configuration file and used for infrastructure and service deployments alike.

Nested YAML structures, override layers, and region-agnostic templating of config values allow sharing common configuration elements across environments and regions. These mechanisms provide the flexibility to adapt settings for a specific cloud, environment, or region when necessary. The configuration structure is enforced by a schema, ensuring the correctness of the configuration while allowing for elaborate override scenarios.

## Nested YAML Structure

ARO HCP configuration data is stored in YAML format, allowing for a structured representation of settings. The configuration supports nested structures, enabling hierarchical organization of properties and logical grouping of related settings.

```yaml
...
frontend:
  cosmosDB:
    name: arohcp-rp
    private: true
    zoneRedundantMode: 'Auto'
  cert:
    name: frontend-cert
    issuer: OneCertV2-PublicCA
...
```

## Override Layers

Configuration properties are embedded into a layered structure within the configuration file. This layering approach enables reusability of common configuration settings while allowing targeted overrides for specific clouds, environments, and regions.

### Layers

- **Default**: This is the base layer of the configuration. It should contain options that apply to most clouds, environments, and regions.
- **Cloud**: This layer holds overrides relevant to a specific Azure cloud, such as `public` or `fairfax`. There is a dedicated cloud named `dev` that can be used to dev purposes in the public cloud. The main purpose of `dev` is to keep `public` cloud settings clean and free of development-related overrides.
- **Environments**: This layer provides overrides for a specific deployment environment within a cloud. Examples include `dev`, `pers`, `integration`, `stage`, and `production`.
- **Region**: This layer holds overrides for a specific region within a deployment environment, allowing for fine-tuned configuration adjustments.

### Base Structure

Below is an example representation:

```yaml
defaults:
  <configuration goes here>
clouds:
  public:
    defaults:
      <configuration goes here>
    environments:
      prod:
        defaults:
          <configuration goes here>
        regions:
          westus3:
            <configuration goes here>
          <other-region>:
            ...
      <other-env>:
        ...
  fairfax:
    ...
```

### Partial Configuration

Configuration in layers can be partial, meaning not all required fields need to be defined in every level if they do not make sense in that context. As long as all layers together eventually provide the necessary fields, the configuration remains valid.

```yaml
defaults:
  frontend:
    cosmosDB:
      private: true                        (1)
      zoneRedundantMode: 'Auto'            (2)
clouds:
  public:
    defaults: {}
    environments:
      dev:
        defaults:
          frontend:
            cosmosDB:
              private: false               (3)
              name: arohcp-rp-dev          (4)
      prod:
        defaults:
          frontend:
            cosmosDB:
              name: arohcp-rp-prod         (5)
```

In this example:

- The global defaults (1) and (2) set `private: true` and `zoneRedundantMode: 'Auto'` for all Cosmos DB instances, as these are generally good settings for almost all Cosmos DB instances.
- The Cosmos DB name is not set in the global defaults, as each deployment requires a unique name.
- In the `public` cloud `dev` environment, the `private` (3) setting is overridden to `false` to make it easier for developers to access their Cosmos DB instance. The DB `name` (4) is introduced at this level, as it is unique to the environment.
- In the `public` cloud `prod` environment, only the `name` (5) is overridden and the inherited `private` and `zoneRedundantMode` settings from the global defaults remain unchanged.

## Region Agnostic Template Variables

Certain configuration fields require unique values within a deployment environment or even an Azure cloud. For example, **Key Vault names** must be unique within an Azure cloud, while **management cluster names** must be unique within a deployment environment.

To prevent repetitive declarations of such values, templating can be used within the configuration. Templating is supported via Go templates in property values, allowing dynamic value substitution based on contextual variables:

- **`ctx.region`**: The full Azure region name.
  - Length: up to 20 characters long.
  - Consists of letters and digits. Starts with a letter.
- **`ctx.regionShort`**: A shorter version of the region name.
  - Length: 2 to 4 characters.
  - Consists of letters and digits. Starts with a letter.
- **`ctx.stamp`**: The numerical value to enumerate the instances of management clusters.
  - Relates to the [EV2 stamp](terminology.md#ev2-stamp).
  - Usually starts with 1
- **`ev2.${ev2 central config config variable}$`**: Additional variables provided by the EV2 central configuration
  - The ARO-Tools repository provides a subset of the real EV2 central configuration as an [additional region agnostic configuration layer](https://github.com/Azure/ARO-Tools/blob/main/pkg/config/ev2config/config.yaml) which works also in RH DEV environment deployments.
  - Variable paths are prefixed with `ev2.` to avoid conflicts with other configuration variables, e.g. `ev2.availabilityZoneCount`
  - Refer to the ARO-Tools [configuration README](https://github.com/Azure/ARO-Tools/blob/main/pkg/config/ev2config/README.md) to learn more about supported EV2 variables and how to add additional ones.
  - EV2 variables can be used for tooling scripts by looking them up with `tooling/templatize/templatize ev2lookup`.

Using these variables, configuration files can remain mostly **region-agnostic**, avoiding almost all regional overrides.

In this example the name of the Cosmos DB instance is defined by using the `ctx.regionShort` template variable. This ensures that the Cosmos DB name is unique within the region.

```yaml
$schema: config.schema.json
defaults:
  frontend:
    cosmosDB:
      ...
      name: arohcp-rp-{{ .ctx.regionShort }}
```

## Configuration Best Practices

- **Avoid Hardcoding**: Use templating to avoid hardcoding values that are unique to a region or environment. Also avoid hardcoding values if they are repeated in various contexts, e.g. a deployment script, Helm chart or Bicep template.
- **Check name restrictions**: Ensure that the names of resources are compliant with Azure naming restrictions, especially when constructing names with templating using `{{ .ctx }}`. We have plans to improve configuration validation to catch these issues automatically.
- **Be aware of the scope of name uniqueness**: Understand the scope of uniqueness for resource names and use appropriate measures like resourcename prefixes and templating. For example
  - Key Vault names must be unique within an Azure cloud - this is an Azure restriction
  - management cluster names must be unique within a deployment environment - this is an architectural restriction

## Using Configuration

Configuration settings can be used in [pipeline files](pipeline-concept.md) and [bicepparam files](bicep.md) to customize service and infra deployments.

### Pipelines

Individual configuration properties can be referenced in pipeline files for use in shell steps:

```yaml
steps:
  - ...
    action: Shell
    variables:
    - name: FRONTEND_COSMOS_DB_NAME
      configRef: frontend.cosmosDB.name
```

For more details on shell steps, refer to the **Shell Step Documentation**.

### Bicep Templates

To use configuration values for Bicep templates, [bicepparam](bicep.md#parameters) files are processed as **Go templates**, allowing configuration lookups using the following syntax:

```bicep
param rpCosmosDbName = '{{ .frontend.cosmosDB.name }}' // quote strings ...
param rpCosmosDbPrivate = {{ .frontend.cosmosDB.private }} // ... but not boolean or number values
```

### Limitations

- Only **basic fields** (string, boolean, or number types) should be referenced from pipeline files and Bicepparam files. Complex data types do not translate well to EV2 configuration settings right now.
- Avoid using **arrays** in configuration. Instead, represent arrays as a list of comma separated values and parse them in Bicep templates using the `csvToArray` function from `modules/common.bicep`. Arrays do not translate well to EV2 configuration settings right now.

## Schema

The structure of the configuration is strictly defined by a [JSON schema](https://json-schema.org/) to ensure correctness, enforce required fields, and enable validation. This schema is maintained in [config.schema.json](../config/config.schema.json) and dictates the format of the YAML configuration, including supported properties, nested structures, and allowed values.

By enforcing a schema, configuration files remain predictable and can be automatically validated before deployment, reducing misconfigurations and ensuring consistency across environments.

## Current Configuration Files

- **[config.yaml](../config/config.yaml)** - Contains the baseline configuration for all ARO HCP environments and Red Hat development environment specific configuration under `clouds.dev`
  - **dev**: integrated DEV environment - the first environment where all services are deployed together.
  - **cspr**: CS PR environment - a dedicated environment for testing Cluster Service PRs.
  - **pers**: personal DEV environment - used by developers to create new personal ARO HCP instances.
  - **perf**: personal perfscale environment - used by the perfscale team to create new ARO HCP instances with production grade management cluster settings
- **[ADO sdp-pipelines/hcp/config.msft.public-cloud-overlay.yaml](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/config.msft.public-cloud-overlay.yaml)** - Contains the configuration overlay for the Microsoft deployment environments, adding MSFT specific settings to the baseline configuration.
  - **int**: MSIT INT environment - a dedicated environment for testing EV2 deployments and MISE.
  - **stg**: MSFT STAGE environment - a dedicated environment for testing AME specific features before promoting to production.

## Update Configuration

1. Update the respective YAML file and run:

   ```sh
   make -C config materialize
   ```

2. Inspect the effects of the changes in the materialized configuration files.
3. Commit the materialized files, open a PR, review, and merge.

Check the section about [Materializing Configuration](#materializing-configuration) and [Propagate Configuration Changes](#propagate-configuration-changes) for more details.

## Propagate Configuration Changes

Propagation of configuration changes varies depending on the environment:

- **[config.yaml](../config/config.yaml)**:
  - Only the **dev** and **cspr** environments are automatically reconciled with new changes for configuration, infrastructure, and service deployments.
  - personal development environments (**pers**) are fully controlled by developers. If there are relevant changes, notify developers so they can apply updates manually.

- **[ADO sdp-pipelines/hcp/config.msft.public-cloud-overlay.yaml](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/config.msft.public-cloud-overlay.yaml)**:
  - This file serves as a `clouds.public` override to the [ARO HCP baseline configuration](../config/config.yaml) for Microsoft environments.
  - Propagation is **not automated**.
  - Refer to the [EV2 deployment documentation](ev2-deployment.md) for details on how to prepare and trigger a deployment.
