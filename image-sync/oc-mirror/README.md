# oc-mirror

This tool packages oc-mirror and all dependencies to mirror OCP artifacts and operators to an ACR.

## Build and push

To build a container image and push it to the service ACR, run

```bash
make build-push
```

## Production deployment

oc-mirror and the required configurations are deployed as Azure Container App
via the `dev-infrastructure/templates/global-image-sync.bicep` template.

## Local dry-run

To run oc-mirror locally, you need to have an active Azure CLI session.

### OCP mirror

To dry-run the OCP mirror, run

```bash
make ocp-dry-run
```

The test mirror-configuration can be found in the `test` directory.

### ACM/MCE mirror

To dry-run the ACM/MCE operator mirror, run

```bash
make acm-dry-run
```

The test mirror-configuration can be found in the `test` directory.
