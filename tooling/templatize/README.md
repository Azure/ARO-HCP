# ARO HCP Templatize Tooling

## Local Development

- Get [config](config) values
- Create output folder if doesn't exist
- Generate output file using input template file and config values
- Command example

For local development we share on subscription. We use the current username as region stamp to generate unique resourcegroup names.

```sh
~/aro/ARO-HCP/tooling/templatize$ go run . generate --config-file="testdata/config.yaml" --input="testdata/helm.sh" --output="output" --cloud="public" --deploy-env="dev" --region="taiwan" --region-stamp=${USER} --cx-stamp="1"

      --config-file string   (Required) config file path
      --input string         (Required) input file path
      --output string        (Required) output file path
      --cloud string         (Required) the cloud (public, fairfax)
      --deploy-env string    (Required) the deploy environment
      --region string        (Required) resources location
      --region-stamp string  (Required) stamp of a region
      --cx-stamp string      (Required) stamp of a CX underlay
```

To inspect the actual config variables available during templating with the `generate` command, use the `inspect` command.

```sh
~/aro/ARO-HCP/tooling/templatize$ go run . inspect --config-file="testdata/config.yaml" --cloud="public" --deploy-env="dev" --region="taiwan" --region-stamp=${USER} --cx-stamp="1"
```

## [Config](config)

- Retrieve values from a single configuration file according to the cloud, environment, and region.
- Replace `region`, `regionStamp` and `cxStamp` values.

## Pipeline validation

From the repository root, validate each topology against its matching configuration:

```sh
make validate-config-pipelines
make validate-config-pipelines-dev-ci
```

Helm validation checks distinct placeholder names across values and namespace files
against the 64-parameter deployment limit, minus the
[reserved parameters defined in ARO-Tools](https://github.com/Azure/ARO-Tools/blob/main/pipelines/types/ev2_parameters.go).

Values and namespace templates are always validated with the resolved configuration.
`--skip-helm-param-budget-validation` skips only the budget check. The dev-CI
Makefile target uses this flag for direct deployments, while the main-topology
target enforces the budget.

Both targets use `--dev-mode` to validate one region. This controls regional
coverage independently of budget enforcement.

### Deployment stack scopes

`make validate-config-pipelines` also runs `pipeline validate-stack-scopes`, which
fails when two executions of the same cloud and environment would deploy the
same `ARMStack` step into the same subscription and resource group.

A deployment stack is identified by its name and the scope it is deployed at (the
resource group, or the subscription for `deploymentLevel: Subscription`), and the
stack name does not include the region or stamp. For every `ARMStack` step (except
`operation: delete`), the check renders the resource group for every region the
step executes in, following `executionConstraints` the way the deployment manifest
generator does, and for stamps 1 and 2 of stamped resource groups. If two
executions land in the same scope, they would share one stack and prune each
other's resources. For example, two regions of one geography listed for a step
whose resource group is per geography:

```yaml
- name: kusto-infra
  resourceGroup: '{{ .kusto.rg }}'   # hcp-kusto-<env>-<geo>
  executionConstraints:
  - environments: [prod]
    regions: [eastus2, centralus]    # both render hcp-kusto-prod-us
```

To fix a collision, restrict the resource group to one execution per scope with
`executionConstraints`, move the step to a region- or stamp-scoped resource
group, or keep the step as `action: ARM`. The check reads the int/stg/prod
environments from `config/config.msft.clouds-overlay.yaml`; stacks defined only
outside this repository are not covered.
