# Slot-bound mock identity operations

## Deployment and repair (Owner only)

This asset is limited to the existing DEV and dev-xs catalog declarations.
No slot count, disabled pool selection, global identity pool size, or personal
development identity changes are part of this rollout.

Use the PR build of `aro-hcp-tests`. The operator needs an active, unconditional
Owner assignment on the customer subscription (or an ancestor), directly to the
invoking principal, plus Entra application/SP management and certificate access
to `https://aro-hcp-dev-svc-kv.vault.azure.net`. Group-only Owner assignments are
not resolved by the preflight. CI credentials must not have these write rights.
Deploy the existing subscription RBAC template first: its subscription-local
`dev-msi-mock-<subscription-id>` role is reused rather than inventing a new role.

```bash
AROHCP_ENV=development make -C test apply-pool-assets ENVIRONMENT=dev POOL=shard0 ASSET=mock_identities
AROHCP_ENV=development make -C test validate-pool-assets ENVIRONMENT=dev POOL=shard0 ASSET=mock_identities
```

Repeat for the intended pools and `ENVIRONMENT=dev-xs`. Unmanaged pools, including
HyperShift, are skipped by default; their subscription Owner must explicitly
select `SUBSCRIPTION='Hypershift Managed Azure'`. Do not run this from CI.
Admission errors print the corresponding Owner repair command. A failed apply
can be rerun: application unique names, SP application IDs, certificate names and
role assignment IDs are stable. Ambiguous or mismatched Graph objects require
Owner investigation; they are never selected arbitrarily or deleted automatically.
Run one Owner reconciliation per selected pool at a time: Key Vault certificate
creation is a lifecycle operation, not a second allocator. CI continues to use
only the primary slot lease for exclusivity and never invokes certificate creation.

Each slot adds exactly six persistent assignments, all at its customer
subscription scope: MSI gets the existing MSI custom role and Key Vault Crypto
User; each ARM helper gets Contributor and Role Based Access Control Administrator.
No assignment is added to the shared infrastructure subscription. Extra historical
assignments are neither removed nor globally audited by this operation.

## Certificates

Existing certificate vault, delivery pipelines and policies are retained. The
asset reuses `tooling/entra-app-credentials/pkg/pinning`: Owner apply creates
missing certificates and pins the current certificate, without rotating healthy
credentials or changing existing certificate policies. Pinning replaces the whole
application `keyCredentials` collection; only this asset's dedicated apps are
eligible. Admission verifies the public certificate's validity and exact pinned
thumbprint, using reads only. Expired certificates fail closed.

Rotation remains an explicit disruptive `entra-app-credentials pin --rotate`
operation, outside deployment pipelines and with affected jobs drained. Use the
deterministic application name as certificate name and `<name>.hcp.osadev.cloud`
as certificate DNS name. Never reset credentials as part of admission or release.

## First-PR upgrade bridge

`openshift/release` wrappers source `aro-hcp-slot.env` before invoking repository
scripts. `provision-from-main.sh` saves the PR's test executable and configuration
adapter before checking out `PULL_BASE_SHA` (or fetched main). It reinstalls only
that adapter into the baseline checkout and invokes the saved generator. Baseline
service images, templates and provisioning remain from main; their existing
`miMock*`, `armHelper*`, and `clustersServiceArmHelper*` config fields receive the
same slot identities as the PR's in-place upgrade. The FPA principal is not the
authenticating ARM helper principal and is never overwritten. Unsupported old
baselines without the adapter seam fail before provisioning, rather than silently
using global identities. The offline baseline test simulates replacement of both
adapter and binary during checkout and compares baseline/upgrade overrides.

`MIMockPrincipalID` uses the bundle for test grants. An explicit
`MI_MOCK_PRINCIPAL_ID` may agree with it but conflicting values fail; without a
bundle, the existing explicit-env, legacy-catalog, config ordering is unchanged.

## Rollout and capacity

The maintainer deploys the asset from this PR, runs e2e-parallel and in-place
upgrade, then asks the HyperShift subscription Owner to deploy after ARO-HCP job
validation. Merge only after those gates. Acquisition requires Graph application
and SP read access, public certificate read access and subscription RBAC read
access; owners must arrange those rights if a selected CI profile lacks them.

The release `aro-hcp-local-e2e`, `aro-hcp-local-e2e-upgrade` workflows and
`aro-hcp-hypershift-deploy` ref still acquire legacy MSI/ARM leases. Those leases
are unused for identity selection once the bundle is present, but remain
concurrency gates. Removing them is a separate openshift/release change; this PR
does not promise increased effective concurrency or remove legacy infrastructure.
Non-slot and personal-dev consumers retain their current fallback behavior.

Subscription budget example from the existing five-full-slot model:
`7615 + 7 shared + 300 legacy + 30 slot-bound = 7952` of 8000 assignments.
Growing the legacy allocation to 492 would instead total 8144. This is arithmetic,
not a measurement of live Azure headroom. Validate actual subscription usage
before deployment. Reducing a declared old pool size does not remove Azure role
assignments. Legacy catalog/identity retirement and capacity demand from public
PR [#8026](https://github.com/Azure/ARO-HCP/pull/8026) are follow-up work.
