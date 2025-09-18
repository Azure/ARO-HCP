## Internal Go client SDK for testing

### Dependencies

Pin the `@autorest/go` version so we control when to upgrade it.

> [!WARNING]
> Upgrading may introduce new TypeSpec compiler validation errors.

``` yaml
use:
- "@autorest/go@4.0.0-preview.74"
```

### API Version

This defines the API version for the client SDK to use.

Before changing this, make sure the new API version has been fully deployed to all
Azure regions by way of the ARO-HCP [ARM manifest](https://msazure.visualstudio.com/AzureRedHatOpenShift/_git/Arm-Manifests).

``` yaml
tag: package-2024-06-10-preview
```

### Tag: package-2024-06-10-preview

These settings apply only when `--tag=package-2024-06-10-preview` is specified on the command line.

``` yaml $(tag) == 'package-2024-06-10-preview'
input-file:
  - redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpclusters/preview/2024-06-10-preview/openapi.json
```

### Code Generation

Other Go SDK generation settings.

``` yaml
go:
  go-sdk-folder: ../test
  module-name: sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp
  module: github.com/Azure/ARO-HCP/test/$(module-name)
  output-folder: $(go-sdk-folder)/$(module-name)
  azure-arm: true
```
