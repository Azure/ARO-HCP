# Multicluster-Engine and Policy installation

This folder contains helm charts and automation to managed helm charts for the ACM components `MCE` and `policy`.

## Structure

Installation and configuration are split into three individual helm charts

* *multicluster-engine-crds:* manages the MCE CRDs
* *multicluster-engine:* manages the MCE operator
* *multicluster-engine-config:* manages the MCE configuration and ACM `policy` components
* *policy:* manages the actual ACM policies and policy placements

## Ingress certificate delivery

The backend selects certificate delivery using the control-plane version and the
persisted `default-ingress-config-<cluster>` ConfigMap desire:

* Versions without the native consumer, including `4.23.0-ec.0`,
  `4.23.0-ec.1` and `5.0`, retain the ACM certificate delivery resources. The
  ConfigMap selects `cluster-ingress-cert`, which the policy fills with the
  service-provider certificate in `openshift-ingress`.
* Covered versions (`4.20` through `4.22` via CPO overrides, `4.23` from
  `4.23.0-ec.2` via the backport, and `5.1` onward natively) use
  `default-ingress-cert` when ACM delivery has not already been selected.

ACM selection remains sticky across upgrades. The native consumer does not
rewrite an existing IngressController certificate reference, so keeping the ACM
resources preserves certificate renewal without requiring an unverified handoff.
The policy accepts only `cluster-ingress-cert` as an explicit selection; an
absent or unrecognized selection uses `default-ingress-cert`.

The policy initializes missing certificate references and repairs service-owned
references (`default-ingress-cert` or `cluster-ingress-cert`). A reference to a
different customer-selected Secret is not enforced, so later policy evaluations
preserve it. ACM Secret delivery and renewal continue independently.

The [version selection](../backend/pkg/controllers/clusterresources/ingress_default_certificate.go)
and [policy](deploy/helm/policies/templates/default-ingress-cert.policy.yaml)
define this contract. E2E checks validate the certificate served over HTTPS,
including trust outside development and the exact customer-provided leaf,
rather than requiring one delivery path or Secret name.

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
