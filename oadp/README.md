# OpenShift API for Data Protection (OADP)

This directory contains the OADP Helm charts and deployment pipeline for HCP backups.

## Charts

There are two Helm charts:

1. **oadp-operator** (`oadp-operator/`) - Deploys the OADP operator itself
2. **hcp-backups** (`deploy/`) - Deploys backup configuration CRs (CloudStorage, DataProtectionApplication, VolumeSnapshotClass)

## Chart Generation

The `oadp-operator` chart is generated from an upstream OLM bundle using the `olm-bundle-repkg` tool.

### Regenerating the Chart

```bash
cd oadp
make generate-chart
```

This uses the configuration in `olm-bundle-repkg-config.yaml`.

Alternatively, run the tool directly from the repo root:

```bash
go run ./tooling/olm-bundle-repkg \
  -c oadp/olm-bundle-repkg-config.yaml \
  -b file://../oadp-operator/bundle/manifests \
  -s oadp-operator-scaffold \
  -o . \
  -l https://github.com/openshift/oadp-operator/tree/master/bundle
```

### Manually Maintained Files

The following CRD files are NOT regenerated and must be maintained manually (fetched from github.com/openshift/api):
- `oadp-operator/crds/0000_03_config-operator_01_securitycontextconstraints.crd.yaml`
- `oadp-operator/crds/0000_10_config-operator_01_infrastructures-Default.crd.yaml`

### Scaffold Templates

The following files are regenerated from `oadp-operator-scaffold/`:
- `oadp-operator/templates/acrpullbinding.yaml` - ACR pull binding for image pulling
- `oadp-operator/templates/azure-backup-storage.secret.yaml` - Azure storage credentials
- `oadp-operator/templates/cluster.infrastructure.yaml` - Cluster infrastructure config for non-OpenShift
- `oadp-operator/values.yaml` - Helm values

### Automated Customizations

The following customizations are automatically applied via `manifestOverrides` in the config:
- WATCH_NAMESPACE environment variable set to `{{ .Release.Namespace }}`
- Azure Workload Identity annotations on ServiceAccounts
- Azure Workload Identity labels on Deployment pods
- Image references parameterized with digest values