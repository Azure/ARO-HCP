# Bicep Templates for ARO HCP

This chapter outlines

* the role of Bicep templates in deploying the required Azure infrastructure for ARO HCP
* the structure of templates, modules and configuration used in this project
* considerations for writing Bicep code that can act in cross-subscription scenarios

## Overview & Purpose

The Bicep templates are responsible for provisioning the infrastructure required for ARO HCP, ensuring that resources across different architectural scopes — such as the Service Cluster Scope and the Management Cluster Scope — are deployed and interconnected correctly. Each scope has its own dedicated infrastructure components, including compute resources, networking configurations, storage solutions, and security mechanisms, all of which must be properly integrated to support the overall system.

Beyond provisioning infrastructure, the Bicep templates also manage access control by configuring managed identities and assigning Azure RBAC roles as needed. This ensures that each deployed component has the correct permissions and authentication mechanisms in place to operate securely within its designated scope. The templates ensure that all required dependencies, identities, and permissions are correctly configured to support reliable system operation.

## Bicep Structure

### Templates

Top-level Bicep templates that correlate to an architectural scope reside in the `templates` directory. These templates are prefixed according to their respective [scope](high-level-architecture.md).

* `global*.bicep` templates for the global scope
* `region*.bicep` templates for the overall region scope
* `svc*.bicep` templates for the service cluster scope
* `mgmt*.bicep` templates for the region cluster scope

There are two additional prefixes for special purposes

* `dev*.bicep` templates that are exclusively required for matters of the DEV environment
* `output*.bicep` templates that allow carrying information between architectural scopes

### Modules

Single bicep templates can become too complex and hard to maintain big at times. To mitigate this, we group resouces by purpose and move them as dedicated bicep files into the `modules` directory.

Modules provide a structured way to organize and reuse infrastructure definitions. While modules enhance maintainability by breaking down complex deployments into manageable components, they also serve a critical function in switching execution context during a Bicep template deployment. See the section about [Cross-Subscription deployments](#cross-subscription-deployments) for more details.

### Parameters

The configuration directory contains Bicep parameter files (`.bicepparam`). Each top-level Bicep template has a corresponding Bicep parameter file to define its configuration. These parameter files are Go templates, which are processed dynamically to generate final configuration files based on the targeted cloud, deployment environment, and region. The configuration references used in these Bicep parameter Go templates adhere to the configuration management concept described in the [configuration documentation](configuration.md).

```bicep
using '../templates/region.bicep'

param globalRegion = '{{ .global.region }}'
param regionalRegion = '{{ .region }}'
```

## Deploying Bicep templates

In ARO HCP, we deploy Bicep templates via [pipeline](pipeline.md) files by supporting a dedicated step type for ARM/Bicep deployments.

> [!IMPORTANT]
> Read the documentation about [pipeline files](pipeline-concept.md) and their general format and functionality. The following documentation covers only the Bicep specific information

```yaml
$schema: "pipeline.schema.v1"

serviceGroup: Microsoft.Azure.ARO.HCP.Region
rolloutName: Region Rollout
resourceGroups:
- name: {{ .regionRG }}                                        (1)
  subscription: {{ .svc.subscription }}                        (2)
  steps:
  - name: region                                               (3)
    action: ARM                                                (4)
    template: templates/my-template.bicep                      (5)
    parameters: configurations/my-template.tmpl.bicepparam     (6)
    deploymentLevel: ResourceGroup/Subscription                (7)
    variables:                                                 (8)
      ...
    [outputOnly: true/false]                                   (9)
```

1. The name of Azure resourcegroup targeted by this deployment
2. The name of the Azue Subscription targeted by this deployment. When deploying via EV2, this needs to reference an [EV2 subscription key](https://ev2docs.azure.net/features/service-artifacts/actions/subscriptionProvisioningParameters.html#subscription-key)
3. The name of the Azure deployment
4. The action type `ARM` marks this step as ARM/Bicep deployment action
5. File reference to the Bicep template, relative to the location of the pipeline file
6. File reference to the Bicep parameter file, relative to the location of the pipeline file. This is a Go template file that will be processed to generate the final parameter file
7. The deployment level for the Bicep template.
8. covered in detail in the [Output templates and output chaining](#output-templates-and-output-chaining) section
9. If `true`, a Bicep step is not allowed to declare any resources and can only provide output by inspecting `existing` resources. See details in the [output templates and output chaining](#output-templates-and-output-chaining) and [dry runs](#dry-runs) sections.

## Cross-Subscription deployments

By default, a Bicep template deployment runs within a specified subscription and resource group. However, when a deployment needs to reach out to another resource group or subscription, modules are the mechanism to switch the deployment scope. Declaring a module and specifying its scope results in the creation of a separate Azure deployment within the targeted subscription and resource group. This approach is essential in various scenarios because the infrastructure for ARO HCP spreads accross various subscriptions and resourcegroups. You can find details about this in [SD-DDR-0051: ARO HCP Azure Deployment Layout](https://docs.google.com/document/d/1a5d-LPbgYMozLyRle7sJRI1h10zDurqFT_jnMGWwRwY/edit?tab=t.0#heading=h.hzsa87ps5uhr).

For example, during the regional DNS zone setup, the deployment also needs to interact with the parent DNS zone located in another resource group—and depending on the environment, even in a different subscription—to set up the zone delegation properly.

Here is the essence of the implementation the the DNS scenario. The [region.bicep](../dev-infrastructure/templates/region.bicep) template accepts the Azure resource ID of the parent zones as input parameter, uses the helper functions in [resource.bicep](../dev-infrastructure/modules/resource.bicep) to extract the subscription and resourcegroup bits from the ID and builds the proper scope from them to run the [zone-delegation.bicep](../dev-infrastructure/modules/dns/zone-delegation.bicep) module within the other scope.

```bicep
// region.bicep
param svcParentZoneResourceId string
import * as res from 'resource.bicep'
var svcParentZoneRef = res.dnsZoneRefFromId(svcParentZoneResourceId)
module regionalSvcZoneDelegation '../modules/dns/zone-delegation.bicep' = {
  name: '${regionalDNSSubdomain}-svc-zone-deleg'
  scope: resourceGroup(svcParentZoneRef.resourceGroup.subscriptionId, svcParentZoneRef.resourceGroup.name)
  params: ...
}
```

## Output templates and output chaining

Not all configuration provided via `bicepparam` files can be statically defined in the [configuration management](configuration.md), e.g. Azure resource IDs containing dynamic subscription IDs that are only known at the time of deployment, as we saw in the example in the [Cross-Subscription deployments](#cross-subscription-deployments) section.

We can pass such dynamic parameter values by chaining multiple bicep modules, one providing the required dynamic values as output and the other one consuming them. This chaining approach is not a Bicep feature but something offered by our pipelines.

In the following example the [region.bicep](../dev-infrastructure/templates/region.bicep) template wants to setup DNS zone delegation from the parent zone to the regional zone. For that it requires the Azure resource ID of the parent zone. The [output-global.bicep](../dev-infrastructure/templates/output-global.bicep) provides the `svcParentZoneResourceId` as an output parameter, while `region.tmpl.bicepparam` asks for it as input parameter. The pipeline hooks up both steps to pass the value from one to the other.

```bicep
// output-global.bicep
...
output svcParentZoneResourceId = svcParentZone.id                (1)

// region.tmpl.bicepparam
param svcParentZoneResourceId = '__svcParentZoneResourceId__'    (2)
```

With this in place we can hook up both bicep templates in a pipeline file.

```yaml
...
resourceGroups:
- name: {{ .global.rg }}                                         (3)
  subscription: {{ .global.subscription }}
  steps:
  - name: global-output
    action: ARM
    template: templates/output-global.bicep
    parameters: configurations/output-global.tmpl.bicepparam
    outputOnly: true                                             (4)
- name: {{ .regionRG }}                                          (5)
  subscription: {{ .svc.subscription }}
  steps:
  - name: region
    action: ARM
    template: templates/region.bicep
    parameters: configurations/region.tmpl.bicepparam
    variables:
      ...
      - name: svcParentZoneResourceId                            (6)
        input:                                                   (7)
          step: global-output
          name: svcParentZoneResourceId
```

1. declare the output
2. declare the input - the value `__svcParentZoneResourceId__` needs to match the name of the parameter. The double-underscore wrapping is a convention required by output chaining to work properly within EV2 (we might want to simplify this in the future)
3. run the `output-global.bicep` template towards the global scope where the parent DNS zone are managed
4. since this is an output only bicep template, we mark it as such. this makes it safe to run during dry-runs
5. run the `region.bicep` template towards the regional scope where the DNS subzones are managed
6. declare the input for the `svcParentZoneResourceId` in the `region.tmpl.bicepparam` parameterfile
7. specify where the input value is coming from

> [!NOTE]
> Why can't we pass in subscription and resourcegroup information to bicepparam files similar to how we do it in steps (3) and (4) as they are apparently part of our [configuration](configuration.md)?
>
> This might work in simple environments, where all subscriptions are known upfront, e.g. in [config.yaml](../config/config.yaml) `global.subscription` is a concrete value pointing to our RH DEV subscription. For more complex scenarios in MSFTs environments (see [config.msft.yaml](../config/config.msft.yaml)), such subscription configuration does not hold actual subscription names or IDs, but symbolic subscription references that make sense for MSFTs deployment orchestration solution EV2. The real subscription IDs are only known during deployment time, when a Bicep template runs within an actual subscription.

## Dry-runs

We leverage ARMs [what-if](https://learn.microsoft.com/en-us/azure/azure-resource-manager/templates/deploy-what-if?tabs=azure-powershell) functionlity to conduct some basic testing on Bicep templates changes before running them towards any environment.

Bicep deployments will not provide any `output` values required for [output chaining](#output-templates-and-output-chaining) when running in `what-if` mode. To mitigate this, depend only on `outputOnly` steps backed by an `output-*.bicep` template. Our pipeline PR checks will execute proper deployments for `outputOnly: true` steps even during pipeline dry-runs, to ensure expected output chaining variables can be passed between steps. There are safeguards in place to ensure an `outputOnly: true` step does not declare and potentially modify any Azure resources.

> [!WARNING]
> `what-if` runs do not replace proper testing as the functionality is limited and heavily depends on the preflight checks of the involved Azure Resource Providers. Don't assume a template will deploy successfuly when it passes a dry-run. But rest assured the deployment will fail with certainty if the dry-run fails.
