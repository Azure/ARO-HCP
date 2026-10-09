# Infrastructure identity leasing

Infrastructure identity leasing lets CI deployments reuse persistent Azure
managed identities instead of creating new identities for every run. The
implemented deployment support is opt-in; pool provisioning, slot-manager
bundle publication, and live CI activation are separate work.

## Deployment behavior

By default, service and management deployments continue creating identities in
their own resource groups. AKS control-plane identities retain the name
`<AKS cluster name>-msi`; existing identity references and RBAC behavior are
unchanged.

With `infrastructureIdentities.useLeased` enabled, deployments reference existing
identities in persistent resource groups within each deployment's subscription.
Cross-subscription references are not supported. Each group supplies the
cluster's workload identities, AKS control-plane identity, and image-puller
identity.

Control-plane identity names belong to the persistent bundle, not the ephemeral
AKS cluster. Changing a CI job's cluster name therefore does not change the
identity it uses. Workload and image-puller identity names continue to come from
the deployment configuration.

Deployments still create federated identity credentials for the current AKS OIDC
issuer and apply RBAC grants at their original scopes. AKS-generated kubelet and
Key Vault Secrets Provider identities are unaffected.

## Configuration contract

The `infrastructureIdentities` configuration selects bundles explicitly:

- `serviceResourceGroup` and `serviceAksIdentityName` identify the service bundle.
- `managementResourceGroups` and `managementAksIdentityNames` map management
  stamp numbers to resource groups and control-plane identities, using
  `1=value,2=value` mappings.

Reuse requires valid resource-group and identity names, including mappings for
the management stamp being deployed. Missing configuration fails validation
rather than falling back to a cluster-derived identity name. These settings do
not create or validate the contents of a persistent identity pool.
