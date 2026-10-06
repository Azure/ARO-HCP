# Reusable Infrastructure Identity Deployment Contract

ARO HCP infrastructure identity reuse is disabled by default. Without an
explicit bundle input, service and management deployments create all managed
identities in their deployment resource groups exactly as before.

The future slot-manager infrastructure identity handler may opt a DEV
deployment into reuse by publishing one shell-escaped JSON value in
`LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE` after acquisition and admission:

```json
{
  "serviceResourceGroup": "persistent-service-identities",
  "managementResourceGroups": {
    "1": "persistent-management-1-identities",
    "2": "persistent-management-2-identities"
  }
}
```

Resource-group names are resolved in the subscription of each deployment
scope. Service-cluster identities must exist in the service deployment
subscription, and each management stamp's identities must exist in that
management deployment's subscription. The contract intentionally does not
support cross-subscription identity references.

Bundle resource-group names must contain 1-90 ASCII letters, digits,
underscores, hyphens, periods or parentheses, and must not end in a period.
Both the CI bundle input and deployment config validate this supported subset
of Azure resource-group names. Empty config values remain valid when reuse is
disabled; a supplied bundle requires non-empty names for every group.

This is a proposed deployment input, not an export implemented by
Azure/ARO-HCP#7104. The lifecycle implementation introduced by that PR exports
`INFRA_SUBSCRIPTION_ID` only when infrastructure assets are demanded; it does
not publish the bundle mapping above or enable infrastructure identity leasing
in the live catalog.

`hack/ci/build-config-override.sh` validates the input and writes the three
deployment settings under `infrastructureIdentities`, setting `useLeased: true`.
Parameter templates pass this flag as `useLeasedInfrastructureIdentities`;
the shared managed-identities module receives `useLeasedIdentities`.
Missing or malformed fields fail config generation. Config validation also requires a mapping for
the management stamp being rendered, so enabling reuse cannot silently fall
back to identity creation.

Each resource group is one complete bundle. Bundle selection is explicit:
`serviceResourceGroup` is the service cluster bundle and every
`managementResourceGroups` key is the management stamp identifier. Resource
group array order and the primary Boskos slot name have no meaning.

The service bundle must contain these canonical roles using the identity names
already configured for the deployment:

- 12 workload identities: frontend, backend, billing, Maestro server, Clusters
  Service, logs, Prometheus, MSI credentials refresher, Admin API, SessionGate,
  custom exporter, and Fleet
- the AKS control-plane identity (`<service AKS name>-msi`)
- `image-puller`

Each management bundle must contain:

- six workload identities: Maestro consumer, management agent, logs,
  Prometheus, Velero, and kube-applier
- the AKS control-plane identity (`<management AKS name>-msi`)
- `image-puller`

The role list is authoritative for deployment wiring, while the configured
identity names remain authoritative for actual Azure names. The future handler
must provision and validate all roles before publication.

In reuse mode Bicep treats the UAMIs as `existing` resources in the current
deployment subscription and supplied resource groups. It still creates per-run federated identity
credentials for the new AKS OIDC issuer and applies the same RBAC grants at
their original target scopes. AKS-generated kubelet and Key Vault Secrets
Provider identities remain unchanged.
