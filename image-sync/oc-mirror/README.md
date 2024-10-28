# oc-mirror

This container contains oc-mirror end required dependencies.

## Example usage for devarohcp

- Build the container image `podman build -t oc-mirror .`
- Alternatively, use `make image`
- Get credentials for Openshift registries https://console.redhat.com/openshift/install/pull-secret
- Get Azure registry credentials `DOCKER_COMMAND=podman az acr login --name arohcpdev`
- Run the sync using the built container

On Linux

```BASH
podman run -it --rm --tmpfs /oc-mirror-workspace \
  -e XDG_RUNTIME_DIR=/ \
  -e STABLE_VERSIONS=4.16,4.17 \
  -e REGISTRY_URL=arohcpdev.azurecr.io \
  -v $HOME/.docker/config.json:/containers/auth.json:Z \
  oc-mirror \
  --dry-run
```

On OSX

```BASH
podman run -it --rm --tmpfs /oc-mirror-workspace \
  -e XDG_RUNTIME_DIR=/ \
  -e STABLE_VERSIONS=4.16,4.17 \
  -e REGISTRY_URL=arohcpdev.azurecr.io \
  -v $HOME/.config/containers/auth.json:/containers/auth.json:Z \
  oc-mirror \
  --dry-run
```

Note, the above command will run the sync in dry-run mode. To run the sync, remove the `--dry-run` flag.
