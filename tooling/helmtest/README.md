
# helmtest

Generate/use golden test files to ensure helm charts are valid.

## Update

Use `UPDATE=true go test -count=1 ./...` to update the golden test files. Note, `-count=1` is usually required.

## Default tests

The test code picks up all Helm steps configured in this repo by iterating over the `../../topology.yaml` file. It then generates fixtures using the default configuration referenced in the `settings.yaml`.

## Tests with custom data

If you want to test certain template features, you can create a custom test by adding a `helmtest_...yaml` file, i.e.:

```yaml
values: ../../values-mgmt.yaml
name: helmtest-kusto-enabled
namespace: arobit
testData:
  kusto:
    enabled: true
    environmentName: test
    ingestionUrl: http://foobar
```

This file is located in the arobit chart directory, in the subfolder `testdata`. It overrides the kusto setting and enables it. This would usually make sense only for MSFT specific environments and enables reviewing template output for all possible scenarios. 

## Regex

You can configure static replacements in the `settings.yaml`, as such:

```yaml
Replace:
  - Regex: 'sha256.[a-fA-F0-9]{64}'
    Replacement: 'sha256:1234567890'
```

these Replacements will be applied on the generated manifest before writing them to disk/verifying them.
