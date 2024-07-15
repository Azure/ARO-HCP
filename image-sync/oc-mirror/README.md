# oc-mirror

This container contains oc-mirror end required dependencies.

## Usage

 * Build the container image ```docker build -t oc-mirror .```
 * Get credentials for Openshift registries https://console.redhat.com/openshift/install/pull-secret
 * Get Azure registry credentials ```az acr login -n <<registry>>```
 * Run the sync using the build container
```BASH
docker run -it --rm --tmpfs /oc-mirror-workspace \
  -e XDG_RUNTIME_DIR=/ \
  -v $PWD/imageset-config.yml:/imageset-config.yml \
  -v $HOME/.docker/config.json:/containers/auth.json \
  docker.io/library/oc-mirror \
  oc mirror --config=/imageset-config.yml docker://devarohcp.azurecr.io --dry-run
```

Note, the above command will run the sync in dry-run mode. To run the sync, remove the `--dry-run` flag.

## Example configuration

The following is an example of the configuration file `imageset-config.yml`.

This exact configuration was used in the initial testing of the `oc-mirror` tool.

```YAML
kind: ImageSetConfiguration
apiVersion: mirror.openshift.io/v1alpha2
storageConfig:
  registry:
    imageURL: devarohcp.azurecr.io/mirror/oc-mirror-metadata
    skipTLS: false
mirror:
  platform:
    channels:
      - name: stable-4.16
        type: ocp
    graph: true
```