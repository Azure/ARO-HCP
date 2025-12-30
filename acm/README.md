# Multicluster-Engine and Policy installation

This folder contains helm charts and automation to managed helm charts for the ACM components `MCE` and `policy`.

## Structure

Installation and configuration are split into three individual helm charts

* *multicluster-engine-crds:* manages the MCE CRDs
* *multicluster-engine:* manages the MCE operator
* *multicluster-engine-config:* manages the MCE configuration and ACM `policy` components
* *policy:* manages the actual ACM policies and policy placements

## Updating charts

To update the MCE and policy charts, change the `acm.mce` and `acm.bundle` configuration in `config/config.yaml` and run `make helm-charts`. Commit the resulting chart changes.

Official release digests for MCE versions can be found [here](https://catalog.redhat.com/software/containers/multicluster-engine/mce-operator-bundle/6160406290fb938ecf6009c6). The digests for `acm.bundle.digest` needs to be compatible with MCE version. For the time being we don't have a better process than to contact the ACM team and ask them for the digest.

It is possible to use pre-release versions of MCE and policy by setting the `acm.mce` and `acm.bundle` configuration in `config/config.yaml` to the desired pre-release version digests. Contract the ACM team to discuss pre-release version usage.

### Additional CRDs

The `make helm-charts` command automatically adds OCM addon CRDs (ManagedClusterAddOn, ClusterManagementAddOn, AddOnDeploymentConfig) from the upstream [open-cluster-management-io/api](https://github.com/open-cluster-management-io/api) repository. These CRDs are required by the MCE operator and policy components but are not included in the MCE operator bundle itself. The `add-ocm-crds.sh` script handles downloading and adding these CRDs to the multicluster-engine-crds chart.
