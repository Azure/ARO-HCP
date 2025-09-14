# RedHatOpenShift HCP Clusters

> see https://aka.ms/autorest

## This is the autorest configuration file for server-side models

This service (ab)uses autorest.go to generate Go models from TypeSpec files.
The generated Go code is intended to be for client-side usage, but can also
benefit the service itself.

---

## Configuration

### Dependencies

Pin the `@autorest/go` version so we control when to upgrade it.

> [!WARNING]
> Upgrading may introduce new TypeSpec compiler validation errors.

``` yaml
use:
- "@autorest/go@4.0.0-preview.73"
```

### Default Version

This is the API version to be generated unless it is overridden on
the command line.

``` yaml
tag: v20240610preview
```

### Basic Information

These are the global settings for generating server-side models.

``` yaml
namespace: redhatopenshift
project-folder: ../internal/api
output-folder: $(project-folder)/$(tag)/generated

go:
  azure-arm: true
  disallow-unknown-fields: true
  # containing-module avoids generating a go.mod or go.version file in
  # output-folder. The containing module name does not matter since we
  # are not generating fakes.
  containing-module: does-not-matter
  generate-fakes: false
```

Additional options to reduce unused code as much as possible.

``` yaml
go:
  inject-spans: false
```

### Tag v20240610preview

These settings apply only when `--tag=v20240610preview` is specified on the command line.

``` yaml $(tag) == 'v20240610preview'
input-file: redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpclusters/preview/2024-06-10-preview/openapi.json
```
