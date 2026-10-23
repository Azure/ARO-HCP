# ARO HCP Templatize Tooling

## Local Development

- Get [config](testdata/config.yaml) values
- Create output folder if doesn't exist
- Generate output file using input template file and config values
- Command example

For local development we share on subscription. We use the current username as region stamp to generate unique resourcegroup names.

```sh
~/aro/ARO-HCP/tooling/templatize$ go run . generate --config-file="testdata/config.yaml" --input="testdata/pipeline.yaml" --output="output" --cloud="public" --deploy-env="dev" --region="taiwan" --stamp=${USER}

      --config-file string           config file path
      --config-file-override string  config file to overlay on --config-file
      --input string                 input file path
      --output string                output file path
      --cloud string                 the cloud (public, fairfax, dev)
      --deploy-env string            the deploy environment
      --ev2-cloud string             the EV2 cloud name
      --region string                resources location
      --region-short-suffix string   suffix to add to short region name, if any
      --stamp string                 stamp
      --extra-args stringToString    extra arguments to be used config templating
      --dev-settings-file string     file to load environment details from
      --dev-environment string       name of the developer environment to use
      --step-cache-dir string        directory where cached step outputs will be stored
      --concurrency int              number of concurrent routines to use when running the pipeline
```

To inspect the actual config variables available during templating with the `generate` command, use the `inspect` command.

```sh
~/aro/ARO-HCP/tooling/templatize$ go run . inspect --config-file="testdata/config.yaml" --cloud="public" --deploy-env="dev" --region="taiwan" --stamp=${USER} --format=yaml
```

## [Config](testdata/config.yaml)

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
