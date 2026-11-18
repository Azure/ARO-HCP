# Dev-CI Topology

The `dev-ci` topology is the standalone, persistent CI-support environment for ARO HCP. It is separate from both the main `Global` / `Region` rollout graph and the on-demand DEV RP footprint that `e2e-parallel` creates inside a job run.

Use this document to understand the responsibilities of the `dev-ci` rollout, slot asset operators, and release-side CI configuration.

## Why Dev-CI Is Separate

`dev-ci` exists because some CI dependencies need to be long-lived and shared across many jobs, but they are not part of the normal product environment topology:

- `config/config-dev-ci.yaml` carries standalone configuration for these CI-supporting resources.
- `topology-dev-ci.yaml` defines a separate rollout graph with two entrypoints: `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` (the unattended postsubmit surface) and `Microsoft.Azure.ARO.HCP.DevCI.Privileged` (the on-demand, Owner-only grants surface).

This keeps persistent CI-support infrastructure separate from the per-job RP footprint that DEV local E2E provisions on demand.

## What The Topology Manages Today

Today `topology-dev-ci.yaml`'s `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint contains one root service group plus five child service groups:

- `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged`
  - Deploys shared dev/CI network resources, the `opstool` AKS cluster, and the shared Prometheus monitoring stack.
- `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota`
  - Deploys the `tenant-quota-collector`, the extensible DEV CI telemetry exporter.
- `Microsoft.Azure.ARO.HCP.DevCI.Gateway`
  - Deploys the shared Istio gateway and DNS wiring for `opstool`.
- `Microsoft.Azure.ARO.HCP.DevCI.CertManager`
  - Deploys `cert-manager` and the shared Azure DNS DNS-01 `ClusterIssuer`.
- `Microsoft.Azure.ARO.HCP.DevCI.CIHealth`
  - Deploys the CIHealth runtime at `cihealth.tools.hcpsvc.osadev.cloud`.
- `Microsoft.Azure.ARO.HCP.DevCI.BatchAnalysis`
  - Provisions storage for Tide-batch analysis output and grants the bot read/write access.

In other words, `dev-ci` owns the persistent CI support layer, not the full runtime behavior of every CI job.

## The Privileged Entrypoint

`topology-dev-ci.yaml` also declares a **standalone, on-demand** privileged tree rooted at the `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint, deliberately kept **separate** from the unattended `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint. It owns six concerns:

1. **Resource provider registration** (`E2ESubscriptionProviders` service group): registering the Azure resource providers that E2E tests depend on across every ARO-HCP-managed E2E subscription. Unlike the RBAC grants, this needs only the `*/register/action` permission, which Contributor already grants — it is **not** an Owner-level operation. It lives here because it is on-demand reconciliation of the same E2E subscription inventory the grants below operate on, and because the unattended postsubmit has no reason to sweep those subscriptions. Both the provider list (`ci.e2eSubscriptionProviders`) and the targets (`ci.<env>.e2eSubscriptions`) come from `config/config-dev-ci.yaml`, so onboarding a subscription there is picked up with no pipeline edit.
2. **The CI bot Entra identity lifecycle** (`E2ESubscriptionRBAC` service group): creating/reconciling the DEV, INT, STG and PROD bot Entra applications + service principals, granting their admin consent, and ensuring their `ci-bot-<env>-{client-secret,client-id,tenant-id}` Key Vault secrets exist (`entra-app-credentials ensure-secret`, which mints a client secret with `addPassword` only when missing; it adds a credential and never removes existing ones, so a previously issued DEV password keeps working until it is deleted by hand). These are Entra **directory-object writes** (create app/SP, `addPassword`) — an unattended CI identity is deliberately not given app/SP-management or credential-minting rights over directory objects, so this runs on demand instead. Nothing in this repository reads the Key Vault copy; the CI jobs consume a mirror in the CI secret store.
3. **The subscription-scoped RBAC grants** (`E2ESubscriptionRBACGrants` service group): the DEV E2E customer-subscription RBAC (custom role definitions + shared-principal role assignments) and the CI bot subscription-scoped RBAC. Its RBAC steps create custom role definitions and/or role assignments, which require **Owner** (or an *unconstrained* User Access Administrator) on the target subscriptions — specifically the rights to write custom role definitions and to assign the privileged `Role Based Access Control Administrator` role to the mock arm-helper principal.
4. **Cost Management tag inheritance** (`E2ESubscriptionRBACGrants` service group): registers `Microsoft.CostManagement` and waits for `Registered` before applying the setting to DEV E2E hosted-cluster subscriptions and the explicitly opted-in E2E infrastructure subscription. The billing step uses direct REST with a GET readback because ARM reports a failed deployment when the provider omits `kind` from its response, despite persisting the setting. The sibling provider-registration branch neither covers the infrastructure subscription nor orders itself before the billing step. The setting applies subscription and resource-group tags to usage records, retaining resource tags on conflicts. The operator needs Cost Management edit permission on all target subscriptions; the global infrastructure subscription is excluded.
5. **The tenant-quota collector (RedHat0) service principal** (`E2ESubscriptionRBAC` and `E2ESubscriptionRBACGrants` service groups): the Entra application `opstool.tenantQuota.tenants.redHat0.applicationName` and its service principal (with the `Organization.Read.All` permission declared) are created declaratively in `E2ESubscriptionRBAC`. Following the CI bot pattern, a Shell step then runs `entra-app-credentials ensure-secret --grant-consent`, which grants the admin consent and stores the client secret (minted only when missing), client ID and tenant ID in the opstool Key Vault. The subscription-scoped `Reader` assignments live in `E2ESubscriptionRBACGrants` and are derived from the same `ci.<env>` subscription lists the collector chart reads, so the grants cannot drift from the collector configuration. The Microsoft test-tenant principal (`testTestAro`) is outside this tenant and remains managed by `tooling/tenant-quota/scripts/manage-service-principals.sh`.
6. **Entra application owners**: the CI bot apps/SPs and the tenant-quota collector app/SP are reconciled with the `aro-hcp-owners` object IDs from `entraAppOwnerIds` (`ownerRelationshipSemantics: 'replace'`), so any OWNERS-group member, not only a tenant App Administrator, can run the privileged entrypoint (Graph requires app *and* service-principal ownership to update them). The list is authoritative: owners that are not in it are removed on the next run, so add new owners to `entraAppOwnerIds` rather than directly in Entra.
   A `check-owners` Shell step (`entra-app-credentials check-owners`) runs before both identity deployments and fails the run when `entraAppOwnerIds` differs from the live members of the `aro-hcp-owners` group (`entraAppOwnersGroupId`). It names the IDs to remove (for example a deleted account, which otherwise makes Graph reject the owner update with an opaque `BadRequest`) and the group members to add; update `entraAppOwnerIds` in `config/config-dev-ci.yaml` as listed and rerun.

**Client secret expiry.** The client secrets minted by `ensure-secret` expire (two years for newly minted ones), and nothing rotates them automatically. Because the privileged entrypoint is run manually by an `aro-hcp-owners` member, `ensure-secret` makes an expired stored secret a hard failure of the run (including `--dry-run`) and logs a warning when it expires within 90 days; the Shell steps above therefore surface a needed rotation. `pin` does the same for Key Vault certificates. To rotate a secret, run the tool directly with `ensure-secret --rotate` (see [`tooling/entra-app-credentials`](../../tooling/entra-app-credentials/README.md#client-secrets)); `--dry-run --rotate` previews the rotation and reports the current expiry without failing.

> Implementation detail: because the topology framework requires every service group to reference a pipeline, the `DevCI.Privileged` root is a **no-op grouping pipeline** (`dev-infrastructure/dev-ci/privileged/pipeline.yaml`, empty step list). Two independent branches hang off that root. `E2ESubscriptionProviders` is a standalone child with no ordering relationship to the RBAC work. The identity service group (`E2ESubscriptionRBAC`) is the other child, with the grants service group (`E2ESubscriptionRBACGrants`) nested beneath it. That nesting matters: the topology runs a parent's steps before its child's, so the identities are created before the grants that look them up via `existing`. Operators never target these service groups directly — always use the entrypoint via the make target below.

The identity that runs the unattended `dev-ci` postsubmit (the `OpenShift Release Bot` service principal, app `38335e22-716a-4a21-bf20-15ab141823f0`) is deliberately **not** an Owner. It holds `Contributor` plus a *condition-constrained* `Role Based Access Control Administrator` / `User Access Administrator` whose Azure ABAC condition forbids assigning the `Owner`, `User Access Administrator`, and `Role Based Access Control Administrator` roles. That is exactly one of the assignments this entrypoint makes, and on some target subscriptions the bot also lacks `Microsoft.Authorization/roleDefinitions/write` — so the grants would fail if the postsubmit tried to apply them. It is likewise not meant to hold the Entra directory-object rights needed to create the bot apps/SPs or mint their secrets. The `DevCI.Privileged` entrypoint is therefore never wired into the unattended `DevCI.Unprivileged` graph and is run manually by a member of the OWNERS group (who has real Owner and the directory privileges to manage these identities) whenever a change touches the bot identities or these grants:

```bash
make dev-ci-privileged-local-run
```

Because the identity service group is the parent of the grants service group within this same entrypoint, a single privileged run creates/reconciles the CI bot identities first and then applies the grants (which look up the service principals via `existing`) — no separate bootstrap run of another entrypoint is required.

This split keeps the blast radius of the standing CI automation small: the postsubmit reconciles only the CI support layer that needs no Owner and no directory-object management, and the rare identity/credential and Owner-only changes are applied on demand.

## What It Does Not Manage

The current `dev-ci` topology intentionally does not own several adjacent pieces of CI:

- Prow jobs, ci-operator configuration, step-registry workflows, and Boskos inventory remain in `openshift/release`.
- The on-demand DEV RP footprint created during local E2E jobs is still provisioned by the release-side workflow, not by `topology-dev-ci.yaml`.
- Dedicated slot mock bundles are prepared by scoped `slot-manager` asset
  commands, not this shared bootstrap topology. See [external preparation and maintenance](external-subscription-onboarding.md).

For the runtime lease model itself, see [CI Identity Leasing](identity-leasing.md).

## Identity Management Responsibilities

The privileged topology prepares the named shared FPA and personal-development
MSI/ARM applications configured in `config/config-dev-ci.yaml`. Its
`pin-mock-certs` and `pin-mock-certs-int` steps create missing certificates and
pin their public keys; its RBAC templates define subscription-local custom
roles and assign the shared principals their roles. Existing certificates and
policies are preserved during routine reconciliation.

Dedicated slot bundles have a separate operator entrypoint:
`make -C test apply-pool-assets` with `ASSET=mock_identities` and explicit pool
selectors. Each primary slot has one MSI mock and two distinct ARM helpers,
with six persistent assignments only on its customer subscription. The shared
bootstrap supplies the custom role definition and FPA grants those slots need.
See [dedicated slot operations](dev-mock-identities.md#dedicated-slot-identity-operations)
and [external subscription preparation](external-subscription-onboarding.md).

`openshift/release` owns the CI workflows, cluster profiles and Boskos inventory.
CI admission validates the prepared mock bundle read-only; it does not perform
Owner reconciliation or certificate rotation.

## Operator Entry Points

Useful local entry points for the current topology:

```bash
make dev-ci-local-run
make dev-ci-privileged-local-run
```

Use the first command for the standalone `dev-ci` postsubmit surface (shared network, `opstool` AKS, monitoring, gateway, cert-manager, CIHealth, quota). Use the second — **an on-demand run performed by an OWNERS-group member, whose RBAC steps require Owner** — when shared bootstrap Entra applications, certificates, pinned credentials, subscription-scoped RBAC, or E2E subscription resource-provider registrations need to be created or reconciled. Certificate rotation is a separate, explicitly disruptive CLI procedure.

## Where To Look

When you need to change or debug the standalone `dev-ci` topology, start here:

- `config/config-dev-ci.yaml`
- `topology-dev-ci.yaml`
- `dev-infrastructure/dev-ci/cluster/opstool-aks-pipeline.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-providers/pipeline.yaml`
- `dev-infrastructure/scripts/e2e-register-providers.sh`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml`
- `dev-infrastructure/configurations/mock-identity-apps.tmpl.bicepparam`
- `dev-infrastructure/configurations/mock-identity-rbac.tmpl.bicepparam`
- `dev-infrastructure/Makefile`
- [CI Identity Leasing](identity-leasing.md)
- [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md)
- [Opstool CI Platform](opstool.md)

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Operations](operations.md)
