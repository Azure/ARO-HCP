# Velero

Velero is used to backup and restore Hosted Clusters, NodePools and Hosted Cluster ETCD instances.

## Installation Overview

Velero is installed using the CLI from the oadp-1.5-latest image. The CLI is used instead of the upstream Helm chart because this installation method vetted by the OADP QA team.
The cli install method references oadp-1.5 image, Azure plugin, and Hypershift plugin images.  Resulting in oadp-1.5 velero server running with the Azure and Hypershift plugins installed.

### Limititions

The Velero CLI does not provide an install flag to add tolerances or node scheduling to pods.
The Velero CLI has a bug where it does not truncate init container names to be less then 63 characters.  See https://github.com/vmware-tanzu/velero/issues/9444.

### How the helm chart works

1. Helm deploys a ConfigMap containing Kustomize patches (hook weight 0)
2. Helm runs the `velero-install` Job (hook weight 1)
3. The Job's init container runs `velero install --dry-run -o yaml` to generate the manifest
4. The Job's main container truncates the init container name to be less then 63 characters, a temporary work around for https://github.com/vmware-tanzu/velero/issues/9444.
5. The Job's main container applies a Kustomize overlay to add node scheduling.
6. The Job's main container then runs `kubectl apply`.

## OADP Partner Information

See [OADP Partner Information](https://github.com/openshift/oadp-operator/blob/oadp-dev/PARTNERS.md) for version compatibility mappings between OpenShift, OADP, and Velero, including supported plugin versions and upgrade workflows.
