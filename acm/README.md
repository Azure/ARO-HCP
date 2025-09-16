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
