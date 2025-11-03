
# helmtest

Generate/use golden test files to ensure helm charts are valid.

## testdata

Create a testdata folder near your chart, i.e. `helmtest_kusto_enabled.yml`

Sample content:

```yaml
# point to the values file used for testing this chart
values: ../values-mgmt.yaml
# name of the test/release
name: helmtest-kusto-enabled
# namespace to use
namespace: arobit
# path to the chart to test
helmChartDir: ../deploy
# override data, test is based on dev config
testData:
  kusto:
    enabled: true
    environmentName: test
    ingestionUrl: http://foobar
```

## Update

Use `UPDATE=true go test -count=1 ./...` to update the golden test files. Note, `-count=1` is usually required.
