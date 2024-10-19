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
