# Dev-CI Topology

The `dev-ci` topology is the standalone, persistent CI-support environment for ARO HCP. It is separate from both the main `Global` / `Region` rollout graph and the on-demand DEV RP footprint that `e2e-parallel` creates inside a job run.

Use this document to understand what the `dev-ci` rollout owns today, what it deliberately does not own, and where the remaining mixed-management boundary still exists.

## Why Dev-CI Is Separate

`dev-ci` exists because some CI dependencies need to be long-lived and shared across many jobs, but they are not part of the normal product environment topology:

- `config/config-dev-ci.yaml` carries standalone configuration for these CI-supporting resources.
- `topology-dev-ci.yaml` defines a separate rollout graph with two entrypoints: `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` (the unattended postsubmit surface) and `Microsoft.Azure.ARO.HCP.DevCI.Privileged` (the on-demand, Owner-only grants surface).
- The long-term goal is for `openshift/release` jobs to consume this shared foundation without defining or deploying the `dev-ci` topology themselves, but that handoff is not complete yet.

This keeps persistent CI-support infrastructure separate from the per-job RP footprint that DEV local E2E provisions on demand.

## What The Topology Manages Today

Today `topology-dev-ci.yaml`'s `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint contains one root service group plus five child service groups:

- `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged`
  - Deploys shared dev/CI network resources, the `opstool` AKS cluster, and the shared Prometheus monitoring stack.
- `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota`
  - Deploys the `tenant-quota-collector` workload that monitors Azure quotas relevant to CI capacity.
- `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC`
  - Reconciles the non-privileged CI bot Entra identities (Graph `Application.ReadWrite.OwnedBy` axis) and rotates their Key Vault secrets. Requires no subscription Owner, so it runs unattended as part of the `DevCI.Unprivileged` entrypoint.
- `Microsoft.Azure.ARO.HCP.DevCI.Gateway`
  - Deploys the shared Istio gateway and DNS wiring for `opstool`.
- `Microsoft.Azure.ARO.HCP.DevCI.CertManager`
  - Deploys `cert-manager` and the shared Azure DNS DNS-01 `ClusterIssuer`.
- `Microsoft.Azure.ARO.HCP.DevCI.CIHealth`
  - Deploys the CIHealth runtime at `cihealth.tools.hcpsvc.osadev.cloud`.

In other words, `dev-ci` owns the persistent CI support layer, not the full runtime behavior of every CI job.

## The Privileged Entrypoint

`topology-dev-ci.yaml` also declares a **standalone, on-demand** privileged tree rooted at the `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint, deliberately kept **separate** from the unattended `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` entrypoint. It applies the DEV E2E customer-subscription RBAC (custom role definitions + shared-principal role assignments) and the CI bot subscription-scoped RBAC. Every step creates subscription-scoped custom role definitions and/or role assignments, which require **Owner** (or an *unconstrained* User Access Administrator) on the target subscriptions — specifically the rights to write custom role definitions and to assign the privileged `Role Based Access Control Administrator` role to the mock arm-helper principal.

> Implementation detail: because the topology framework requires every service group to reference a pipeline, the `DevCI.Privileged` root is a **no-op grouping pipeline** (`dev-infrastructure/dev-ci/privileged/pipeline.yaml`, empty step list) and the actual grants live in a child service group. Operators never target that child directly — always use the entrypoint via the make target below.

The identity that runs the unattended `dev-ci` postsubmit (the `OpenShift Release Bot` service principal, app `38335e22-716a-4a21-bf20-15ab141823f0`) is deliberately **not** an Owner. It holds `Contributor` plus a *condition-constrained* `Role Based Access Control Administrator` / `User Access Administrator` whose Azure ABAC condition forbids assigning the `Owner`, `User Access Administrator`, and `Role Based Access Control Administrator` roles. That is exactly one of the assignments this entrypoint makes, and on some target subscriptions the bot also lacks `Microsoft.Authorization/roleDefinitions/write` — so the grants would fail if the postsubmit tried to apply them. The `DevCI.Privileged` entrypoint is therefore never wired into the unattended `DevCI.Unprivileged` graph and is run manually by a member of the OWNERS group (who has real Owner) whenever a change touches these grants:

```bash
make dev-ci-privileged-local-run
```

This grants rollout looks up the CI bot service principals via `existing`, so the `DevCI.Unprivileged` entrypoint must have run at least once first — it creates those identities.

This split keeps the blast radius of the standing CI automation small: the postsubmit reconciles everything that does not need Owner, and the rare Owner-only changes are applied on demand.

## What It Does Not Manage

The current `dev-ci` topology intentionally does not own several adjacent pieces of CI:

- Prow jobs, ci-operator configuration, step-registry workflows, and Boskos inventory remain in `openshift/release`.
- The on-demand DEV RP footprint created during local E2E jobs is still provisioned by the release-side workflow, not by `topology-dev-ci.yaml`.
- Static consumer artifacts such as `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` are still generated separately.
- The Key Vault **certificates** backing the mock identities are created by a separate `make create-mock-identity-certs` step, not by the Bicep templates (the Entra apps trust them via SNI but cannot create them — Bicep cannot create Key Vault certificates).

For the runtime lease model itself, see [CI Identity Leasing](identity-leasing.md).

## The Current Mixed-Management Boundary

The DEV MSI mock service-principal pool used by local E2E jobs is now managed declaratively, with only two narrow hand-offs left.

- The `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint owns the pool end to end on the Azure side:
  - `dev-infrastructure/templates/mock-identity-apps.bicep` creates/updates the pooled Entra apps and service principals (looping `.ci.dev.mockIdentities.pool.size` times) with SNI certificate auth.
  - `dev-infrastructure/templates/mock-identity-rbac.bicep` resolves each principal's object ID via Microsoft Graph and applies the home- and E2E-subscription grants. Principal IDs are no longer stored in `config/config-dev-ci.yaml`. Because those grants require subscription Owner, they are applied on demand rather than by the postsubmit (see [The Privileged Entrypoint](#the-privileged-entrypoint)).
- What remains outside the rollout:
  - The Key Vault **certificates** the apps trust are created by `make create-mock-identity-certs`, which calls `dev-infrastructure/scripts/create-kv-cert.sh` (`az keyvault certificate create`). Bicep cannot create Key Vault certificates, so this is a separate idempotent step rather than part of the template — SNI trust is declared in `mock-identity-apps.bicep`, the certs are created here (see [DEV Mock Identities → Certificates](dev-mock-identities.md#certificates)).
  - `make populate-msi-mock-pool` performs live Entra lookups and writes the static `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` catalog that release-side jobs consume.
  - `openshift/release` still owns the Boskos inventory and lease contract for the `aro-hcp-msi-mock-cs-sp-dev` resource type.

So the Entra objects and their RBAC are single-sourced in the topology; the remaining spread is limited to the certificate step and the release-side Boskos/catalog wiring.

## Long-Term Direction

The Entra-object and RBAC half of the intended end state is now in place — a single declarative producer (`mock-identity-apps.bicep` + `mock-identity-rbac.bicep`) owns the pool lifecycle on the Azure side, driven by one source of truth in `config/config-dev-ci.yaml`. The remaining work to close the loop:

- generate downstream consumer artifacts (the static pool catalog and the release-side Boskos inventory) from that same source instead of updating them separately
- fold certificate provisioning into the rollout itself, so operators don't run a separate `make create-mock-identity-certs` step (Bicep can't create Key Vault certificates today, so cert creation lives in that idempotent script)

Until those are designed and validated, the certificate and release-side hand-offs above remain the supported operating model.

## Operator Entry Points

Useful local entry points for the current topology:

```bash
make dev-ci-local-run
make dev-ci-privileged-local-run
```

Use the first command for the full standalone `dev-ci` entrypoint, which includes the non-privileged CI bot identity/secret rollout. Use the second — **an Owner-only, on-demand run performed by an OWNERS-group member** — when the subscription-scoped custom roles and role assignments need to be applied.

## Where To Look

When you need to change or debug the standalone `dev-ci` topology, start here:

- `config/config-dev-ci.yaml`
- `topology-dev-ci.yaml`
- `dev-infrastructure/dev-ci/cluster/opstool-aks-pipeline.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml`
- `dev-infrastructure/configurations/mock-identity-apps.tmpl.bicepparam`
- `dev-infrastructure/configurations/mock-identity-rbac.tmpl.bicepparam`
- `dev-infrastructure/Makefile`
- `dev-infrastructure/openshift-ci/populate-msi-mock-pool.sh`
- [CI Identity Leasing](identity-leasing.md)
- [CI Quota Monitoring](quota-monitoring.md)
- [Opstool Cluster Guide](../ops/opstool-cluster-guide.md)

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Operations](operations.md)
