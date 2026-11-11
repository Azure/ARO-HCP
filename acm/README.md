# Multicluster-Engine and Policy installation

This folder contains helm charts and automation to managed helm charts for the ACM components `MCE` and `policy`.

## Structure

Installation and configuration are split into three individual helm charts

* *multicluster-engine-crds:* manages the MCE CRDs
* *multicluster-engine:* manages the MCE operator
* *multicluster-engine-config:* manages the MCE configuration and ACM `policy` components
* *policy:* manages the actual ACM policies and policy placements

## Retired ingress certificate policy

The policy chart no longer installs the `default-ingress-config` Policy or its
`default-ingress-cert` PlacementBinding. The shared `all-hosted-clusters` Placement
and `hypershift-management-clusters` ManagedClusterSetBinding remain unchanged.

Removing the policy stops ACM delivery and renewal of the guest
`openshift-ingress/cluster-ingress-cert` Secret; it does not intentionally delete
that existing Secret. The [retired ConfigurationPolicy](https://github.com/Azure/ARO-HCP/blob/6cc76972270bcfba19dc4c6b2a6cf641b22226be/acm/deploy/helm/policies/templates/default-ingress-cert.policy.yaml#L41) explicitly used
`pruneObjectBehavior: None`, and its Secret template supplied no owner references.
The [inspected release-2.14 controller's deletion path](https://github.com/stolostron/config-policy-controller/blob/3b90e449185306f906bd4f44210efb1df5b93cf9/controllers/configurationpolicy_controller.go#L1217)
does not prune objects in that mode, and its
[creation path](https://github.com/stolostron/config-policy-controller/blob/3b90e449185306f906bd4f44210efb1df5b93cf9/controllers/configurationpolicy_controller.go#L2748)
does not add an owner reference to the Secret. The hub source Secret
`open-cluster-management-policies/default-ingress-tls-cert-<cluster>` was only read
by the policy, not created by this chart. Neither that Secret nor the guest
`default-ingress-cert` Secret is deleted by this chart change.

This preserves existing certificate material as a rollback aid, not a guarantee
of continued renewal or certificate validity. Retention relies on the deployed
policy retaining its documented pruning mode and on no other owner/controller
deleting the Secret or its namespace. These are source-level lifecycle semantics,
not verification of deployed controller binaries, live Secret metadata, or
production state. Restoring the retired policy is a separate rollback operation;
this change does not modify Secret contents, ownership, or the remaining
certificate delivery controllers.

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
