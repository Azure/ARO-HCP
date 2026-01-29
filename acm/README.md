# Multicluster-Engine and Policy installation

This folder contains helm charts and automation to managed helm charts for the ACM components `MCE` and `policy`.

## Structure

Installation and configuration are split into three individual helm charts

* *multicluster-engine-crds:* manages the MCE CRDs
* *multicluster-engine:* manages the MCE operator
* *multicluster-engine-config:* manages the MCE configuration and ACM `policy` components
* *policy:* manages the actual ACM policies and policy placements

## Updating charts

To update the MCE and policy charts, change the `acm.mce.bundle` and `acm.operator.bundle` digests in `config/config.yaml` and run `make helm-charts`. Commit the resulting chart changes.

The ACM version is automatically extracted from the ACM operator bundle image at build time. The version is used to:
- Clone the appropriate upstream branch (`release-x.y`)
- Extract the correct image manifest (`extras/x.y.z.json`)
- Set helm chart versions

Official release digests can be found here:
- MCE: [multicluster-engine/mce-operator-bundle](https://catalog.redhat.com/software/containers/multicluster-engine/mce-operator-bundle/6160406290fb938ecf6009c6)
- ACM: Contact the ACM team for compatible ACM operator bundle digests

It is possible to use pre-release versions of MCE and ACM by setting the `acm.mce.bundle` and `acm.operator.bundle` digests in `config/config.yaml` to the desired pre-release version digests. Contact the ACM team to discuss pre-release version usage.
