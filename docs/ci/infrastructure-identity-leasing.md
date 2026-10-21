# Infrastructure identity leasing

Infrastructure identity leasing lets CI deployments reuse persistent Azure
managed identities instead of creating new identities for every run. The
implemented deployment support is opt-in. Slot-manager can acquire, admit,
provision, and publish a leased bundle when a catalog declares the asset.
Live catalog demand, pool creation, and CI activation are separate work.

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

## CI override generation

`test/aro-hcp-tests infra build-config-override` generates CI configuration
overrides offline, without Azure credentials or E2E suite initialization. The
sourced `hack/ci/build-config-override.sh` adapter uses the existing binary at
`test/aro-hcp-tests` in the source checkout; `ARO_HCP_TESTS_BINARY` can select an
explicit binary for local use.

The root CLI still validates `ARO_HCP_SUITE_PARALLELISM` during construction;
when supplied, it must be empty or a positive integer, even for offline commands.

Commands supplied by openshift-tests-extension initialize suites before running;
only `run-suite` and `run-test` configure the MI scheduler, and only `run-suite`
applies suite concurrency defaults. Help and offline infrastructure commands
skip suite initialization.

`LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE` supplies a JSON object with the four
resource-group and identity-name fields above. Its management fields are objects
keyed by stamp number and must contain the same non-empty set of stamps. A valid
bundle enables reuse; without it, no infrastructure identity override is emitted.

The command preserves service-image, MSI mock, and ARM-helper overrides, merges
Hypershift overrides last, and publishes `config-override.yaml` in the shared
directory only after all inputs pass validation. Unrecognized `*_IMAGE`
environment variables fail generation rather than silently ignoring new or
misspelled image overrides. Go lease publishers and consumers share the
environment contract in `test/pkg/leaseenv`, including core slot metadata,
subscription and region selection, VM family policy, and asset-specific leases.

CI image overrides also supply their source registries to
`USE_OC_LOGIN_REGISTRIES`. The sourced adapter exports the resulting list so
ARO-Tools can authenticate with `oc registry login` when mirroring those images
into ACR. This authentication handoff is derived from the image overrides, not
a separate CLI option.

Baseline provisioning retains scripts, configuration, and catalogs from the
selected baseline revision. Templatize validates the merged configuration
against its schema during provisioning.

For a local invocation after building `test/aro-hcp-tests`:

```bash
DEPLOY_ENV=ci01 SHARED_DIR=/path/to/existing/shared-directory \
  test/aro-hcp-tests infra build-config-override --source-dir "$PWD"
```
