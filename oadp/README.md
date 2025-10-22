# Openshift Application Data Protection (HCP Backups)

## Chart creation
This is the OADP helm chart/deployment pipeline. The helm chart is temporary until upstream provides an official helm chart.

### Regenerating the Chart

The preferred method to regenerate the helm chart is using the Makefile:

```bash
cd oadp
make regenerate-chart
```

This uses the olm-bundle-repkg tooling with the configuration in `oadp/olm-bundle-repkg-config.yaml`.

Alternatively, you can run the tool directly from the repo root:

```bash
go run ./tooling/olm-bundle-repkg \
  -c oadp/olm-bundle-repkg-config.yaml \
  -b file://../oadp-operator/bundle/manifests \
  -o oadp \
  -l https://github.com/openshift/oadp-operator/tree/master/bundle
```

### Manual Customizations

The following files are manually added and must be preserved after regeneration:

**Additional CRDs** (not in the OLM bundle, fetched from github.com/openshift/api):
- `oadp-operator/crds/0000_03_config-operator_01_securitycontextconstraints.crd.yaml`
- `oadp-operator/crds/0000_10_config-operator_01_infrastructures-Default.crd.yaml`
- `oadp-operator/crds/routes.crd.yaml`

**Custom Templates**:
- `oadp-operator/templates/acrpullbinding.yaml` - ACR pull binding for image pulling
- `oadp-operator/templates/azure-backup-storage.secret.yaml` - Azure storage credentials
- `oadp-operator/templates/cluster.infrastructure.yaml` - Cluster infrastructure config for non-OpenShift

### Automated Customizations

The following customizations are automatically applied via `manifestOverrides` in the config:
- WATCH_NAMESPACE environment variable set to `{{ .Release.Namespace }}`
- Azure Workload Identity annotations on ServiceAccounts
- Azure Workload Identity labels on Deployment pods
## Deployment
```

```