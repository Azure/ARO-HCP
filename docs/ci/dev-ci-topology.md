# Dev-CI Topology

The `dev-ci` topology is the standalone, persistent CI-support environment for ARO HCP. It is separate from the main `Global` / `Region` rollout graph and from the on-demand DEV footprint that `e2e-parallel` creates inside a job run. This document explains what the `dev-ci` rollout owns, why part of it can only be run by hand, and what it leaves to other systems.

## Why Dev-CI Is Separate

Some CI dependencies have to be long-lived and shared by many jobs, but they are not part of any product environment. `dev-ci` gives them their own configuration (`config/config-dev-ci.yaml`) and their own rollout graph (`topology-dev-ci.yaml`) with two entrypoints:

- `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged` is the unattended surface. The `branch-ci-Azure-ARO-HCP-main-dev-ci-pipeline-postsubmit` Prow job rolls it out after changes to `dev-ci` config, topology, `dev-infrastructure/` or `tooling/` merge.
- `Microsoft.Azure.ARO.HCP.DevCI.Privileged` is the on-demand surface for changes that need Owner or Entra directory rights. See [The Privileged Entrypoint](#the-privileged-entrypoint).

The longer-term goal is for `openshift/release` jobs to consume this shared foundation without defining or deploying any of it; that handoff is not complete.

## What The Topology Manages Today

The unattended entrypoint is rooted at `Microsoft.Azure.ARO.HCP.DevCI.Unprivileged`, which deploys the shared network resources, the `opstool` AKS cluster and the Prometheus monitoring stack (see [Opstool CI Platform](opstool.md)). Workloads hang off it as child service groups:

- `TenantQuota`: the DEV CI telemetry exporter (see [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md)).
- `Gateway`: the shared Istio gateway and DNS wiring for `opstool`.
- `CertManager`: `cert-manager` and the shared DNS-01 `ClusterIssuer`.
- `CIHealth`: the CIHealth runtime at `cihealth.tools.hcpsvc.osadev.cloud`.
- `BatchAnalysis`: the storage account that receives Tide-batch analysis output, with access for the analysis bot.

[`topology-dev-ci.yaml`](../../topology-dev-ci.yaml) is the authoritative list; this one can lag behind it. In short, `dev-ci` owns the persistent CI support layer, not the runtime behaviour of any individual job.

## The Privileged Entrypoint

The unattended postsubmit runs as the `OpenShift Release Bot` service principal. That identity is deliberately not an Owner: it has `Contributor` plus a condition-constrained `Role Based Access Control Administrator` that cannot hand out `Owner`, `User Access Administrator` or `Role Based Access Control Administrator`, and it holds no rights over Entra directory objects. Several things the CI environment depends on need more than that: custom role definitions, privileged role assignments, and creating Entra applications and minting their secrets. Rather than widen the bot's standing permissions, those pieces live behind a separate entrypoint, `Microsoft.Azure.ARO.HCP.DevCI.Privileged`, that is never part of the unattended graph and is run by hand by a member of the `aro-hcp-owners` group, who has the required Owner and Entra privileges. Changes to the bot identities or to any of the grants below are applied this way, and the rest of the postsubmit stays low-risk.

The privileged entrypoint reconciles five groups of things:

- **Resource-provider registration** on every E2E subscription, driven by `ci.e2eSubscriptionProviders` and the per-environment `ci.<env>.e2eSubscriptions` lists in the config. Registration only needs Contributor, but it sits here because it reconciles the same subscription inventory as the grants, which the postsubmit has no reason to sweep. Onboarding a subscription means editing the config, not the pipeline.
- **CI bot identities**: the DEV, INT, STG and PROD bot Entra applications and service principals, their admin consent, and their client secret, client ID and tenant ID in the `opstool` Key Vault. Nothing in this repository reads that copy; CI jobs consume a mirror in the CI secret store. The profile each application backs, and the one application that backs two profiles, are listed in the [cluster profile secret contract](cluster-profile-secret-contract.md#rotation).
- **The tenant-quota collector principal** for the Red Hat tenant, created and consented the same way as the bots, plus a `Reader` assignment on every subscription the collector monitors. The assignments are derived from the same `ci.<env>` subscription lists the collector chart reads, so grants and collector configuration cannot drift. The Microsoft test-tenant principal lives outside this tenant and is still managed by `tooling/tenant-quota/scripts/manage-service-principals.sh`.
- **The named DEV and INT mock identities**: their Entra applications, certificates (created if missing and pinned on the application) and the RBAC that lets them act on E2E subscriptions. What each identity stands in for is explained in [DEV Mock Identities](dev-mock-identities.md). The per-slot dedicated mock identities used by slot-manager jobs are not part of this rollout; operators prepare them as described in [Slot Mock Identities](dev-mock-identities.md#slot-mock-identities).
- **Subscription-scoped settings**: the bot RBAC on E2E subscriptions, the `createdAt` resource-group tag policy, and Cost Management tag inheritance on the DEV hosted-cluster subscriptions and the opted-in E2E infrastructure subscription.

A few behaviours are worth knowing before running it:

- Identities are applied before the grants that look them up, because the grants service group is nested under the identity one and the topology runs parents first. One run of the entrypoint is enough from scratch; there is no separate bootstrap. The root service group itself (`DevCI.Privileged`) is an empty grouping pipeline, since every service group must reference a pipeline. Always target the entrypoint, never the individual service groups.
- Entra application owners come from `entraAppOwnerIds` in the config and are applied with replace semantics, so the list is authoritative: add new owners there, not directly in Entra. A `check-owners` step fails the run before any Entra write if the list differs from the live members of `aro-hcp-owners`, and it names the IDs to remove and the members to add. Update the config as it says and rerun.
- Client secrets minted for the bots and the collector expire after two years and nothing rotates them. An expired stored secret fails the run, and one expiring within 90 days logs a warning, so a run is also how you find out a rotation is due. To rotate, use the tool directly as described in [`tooling/entra-app-credentials`](../../tooling/entra-app-credentials/README.md#expiry-and-rotation); certificates follow the same policy.
- Cost Management tag inheritance needs Cost Management edit permission on each target subscription in addition to Owner, and the global infrastructure subscription is excluded. The step calls the REST API directly because ARM reports a failed deployment even though the setting is persisted (see the comment in `dev-infrastructure/scripts/e2e-enable-cost-tag-inheritance.sh`).

## What It Does Not Manage

Prow jobs, ci-operator configuration, step-registry workflows and Boskos inventory stay in `openshift/release`. The on-demand DEV footprint created during a job run is provisioned by the release-side workflow, not by this topology. Slot-manager's catalog and assets are covered by the [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md) and [Slot Assets](slot-assets.md).

## Operator Entry Points

Roll out the unattended surface locally:

```bash
make dev-ci-local-run
```

Roll out the privileged surface. This is an on-demand run by an `aro-hcp-owners` member; it builds `tooling/entra-app-credentials` first and uses the invoking user's Azure CLI credentials:

```bash
make dev-ci-privileged-local-run
```

Certificate rotation is a separate, deliberately disruptive procedure documented in [DEV Mock Identities](dev-mock-identities.md).

## Where To Look

- `topology-dev-ci.yaml` and `config/config-dev-ci.yaml` for the service groups and their configuration.
- `dev-infrastructure/dev-ci/` for the pipelines of every service group.
- [Opstool CI Platform](opstool.md) and [DEV CI Monitoring and Alert Response](dev-ci-monitoring.md) for the cluster and the telemetry it hosts.
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md) for adding an E2E subscription.
- [CI Overview](README.md) for the rest of the CI documentation.
