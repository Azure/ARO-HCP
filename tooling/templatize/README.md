# ARO HCP Templatize Tooling

## Local Development
- Get [config](config) values
- Create output folder if doesn't exist
- Generate output file using input template file and config values
- Command example
```
chiac@CHIAC:~/aro/ARO-HCP/tooling/templatize$ go run . --config-file="testdata/config.yaml" --input="testdata/helm.sh" --output="output" --cloud="public" --deploy-env="dev" --region="taiwan" --user="chiac"

      --config-file string   (Required) config file path
      --input string         (Required) input file path
      --output string        (Required) output file path
      --cloud string         (Required) the cloud (public, fairfax)
      --deploy-env string    (Required) the deploy environment
      --region string        (Required) resources location
      --user string          unique user name
```

## [Config](config)
- Retrieve values from a single configuration file according to the cloud, environment, and region.
- Replace `region` and `user` values.