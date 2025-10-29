
# helmtest

Generate/use golden test files to ensure helm charts are valid.

## .helmtest

Add your testfiles to `.helmtest.yaml` to ensure it is picked up.

Specify `setFile` and add the required parameters (values file can not be used with template mode). The format for this file is `key: value`. Where key is the parameter as you would use it in CLI with `--set` parameter.

## Update

Use `UPDATE=true go test -count=1 ./...` to update the golden test files. Note, `-count=1` is usually required.
