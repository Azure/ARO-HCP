# Dev-CI Topology

The `dev-ci` topology is the standalone, persistent CI-support environment for ARO HCP. It is separate from both the main `Global` / `Region` rollout graph and the on-demand DEV RP footprint that `e2e-parallel` creates inside a job run.

Use this document to understand what the `dev-ci` rollout owns today, what it deliberately does not own, and where the remaining mixed-management boundary still exists.

## Why Dev-CI Is Separate

`dev-ci` exists because some CI dependencies need to be long-lived and shared across many jobs, but they are not part of the normal product environment topology:

- `config/config-dev-ci.yaml` carries standalone configuration for these CI-supporting resources.
- `topology-dev-ci.yaml` defines a separate rollout graph rooted at `Microsoft.Azure.ARO.HCP.DevCI.Infra`.
- The long-term goal is for `openshift/release` jobs to consume this shared foundation without defining or deploying the `dev-ci` topology themselves, but that handoff is not complete yet.

This keeps persistent CI-support infrastructure separate from the per-job RP footprint that DEV local E2E provisions on demand.

## What The Topology Manages Today

Today `topology-dev-ci.yaml` contains one entrypoint, `Microsoft.Azure.ARO.HCP.DevCI.Infra`, plus five child service groups:

- `Microsoft.Azure.ARO.HCP.DevCI.Infra`
  - Deploys shared dev/CI network resources, the `opstool` AKS cluster, and the shared Prometheus monitoring stack.
- `Microsoft.Azure.ARO.HCP.DevCI.TenantQuota`
  - Deploys the `tenant-quota-collector` workload that monitors Azure quotas relevant to CI capacity.
- `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC`
  - Manages explicit DEV E2E customer-subscription RBAC and the shared custom-role assignable scopes used by those subscriptions.
- `Microsoft.Azure.ARO.HCP.DevCI.Gateway`
  - Deploys the shared Istio gateway and DNS wiring for `opstool`.
- `Microsoft.Azure.ARO.HCP.DevCI.CertManager`
  - Deploys `cert-manager` and the shared Azure DNS DNS-01 `ClusterIssuer`.
- `Microsoft.Azure.ARO.HCP.DevCI.CIHealth`
  - Deploys the CIHealth runtime at `cihealth.tools.hcpsvc.osadev.cloud`.

In other words, `dev-ci` owns the persistent CI support layer, not the full runtime behavior of every CI job.

### CI Bot Identity Management

The `E2ESubscriptionRBAC` service group also manages CI bot service principals ("OpenShift Release Bot" variants) declaratively via Bicep + Microsoft Graph. Three pipeline steps handle the lifecycle:

1. **ci-bot-identity** (ARM) — Creates the Entra application and service principal using the Microsoft Graph Bicep extension.
2. **ci-bot-secret** (Shell) — Generates a client secret via the Graph API and stores `client-id`, `client-secret`, and `tenant-id` in the dev-ci Azure Key Vault. Idempotent: skips if the KV secret already exists.
3. **ci-bot-rbac** (ARM) — Fans out subscription-scoped RBAC grants (Contributor, RBAC Administrator with ABAC condition, AKS RBAC Cluster Admin) across all E2E and infrastructure subscriptions for that environment. On subscriptions marked `isGlobalSubscription`, additionally grants Key Vault Administrator and Grafana Admin.

The Shell step runs as the pipeline executor (human or OpenShift Release Bot), which already has the required Graph and Key Vault permissions — no additional managed identity is needed.

#### What the pipeline manages vs config-only inventory

The `ci` section in `config/config-dev-ci.yaml` is keyed by environment. An environment's bot is only created and granted RBAC when it is listed in the bicepparam templates. Today **only STG is actively managed**:

- `ci-bot-identity.tmpl.bicepparam` creates the "OpenShift Release Bot - STG" app + SP.
- `ci-bot-rbac.tmpl.bicepparam` grants RBAC on STG E2E subscriptions.
- `ci-bot-ensure-secret.sh` generates and stores the STG client secret.

The `ci.dev` entry is **config-only inventory** — no pipeline step acts on it:

- `ci.dev.bot.applicationName` records the existing "OpenShift Release Bot" (still managed imperatively via `grant-openshift-release-bot-dev.sh`).
- `ci.dev.e2eSubscriptions` is consumed by the existing mock identity RBAC steps (`e2e-subscription-rbac-assignments.tmpl.bicepparam`, `mock-identity-roles.tmpl.bicepparam`, `dev-operator-roles.tmpl.bicepparam`). This is a config path rename, not new behavior.
- `ci.dev.infrastructureSubscriptions` is pure inventory, not referenced by any pipeline step.
- `ci.dev.devMockIdentities` is consumed by the existing mock identity RBAC steps (path rename from the old `devCi.e2eSubscriptionRbac` structure).

Running the pipeline today will **not** create, modify, or conflict with the existing DEV Release Bot or its role assignments.

#### Prerequisites

The pipeline executor must have:
- **Graph API access** — The ability to call `applications/{id}/addPassword` on applications it owns (`Application.ReadWrite.OwnedBy` app role, or being an owner of the application).
- **Key Vault Secrets Officer** (or equivalent) on the target Key Vault (`opstool-kv-*`).

#### Admin consent

The `ci-bot-secret` step attempts to grant admin consent for the bot's declared API permissions (currently `Application.ReadWrite.OwnedBy`). This is a best-effort call: if the pipeline executor does not have a sufficiently privileged Entra role (Cloud Application Administrator, Privileged Role Administrator, or Global Administrator), the call will fail with a warning.

**This warning is not self-healing.** Subsequent pipeline runs will not fix it. A tenant admin must grant consent manually:

```bash
az ad app permission admin-consent --id <app-client-id>
```

Until consent is granted, the bot cannot exercise `Application.ReadWrite.OwnedBy`, which means E2E tests that create app registrations (e.g. external auth tests) will fail.

#### First-run propagation delay

On the very first deployment of a new bot, there can be a propagation delay between Entra (where the SP is created by `ci-bot-identity`) and ARM (where `ci-bot-rbac` tries to assign roles to it). If `ci-bot-rbac` fails with a "principal not found" error, simply re-run the pipeline — the SP will have propagated by then. This is a one-time bootstrapping issue and does not affect subsequent runs.

#### Post-deployment: Vault secret sync

After the first successful deployment, the client credentials are in the Azure Key Vault but must be manually transferred to the CI Vault:

1. Retrieve from Azure KV:
   ```bash
   az keyvault secret show --vault-name opstool-kv-wus3 --name ci-bot-stg-client-id --query value -o tsv
   az keyvault secret show --vault-name opstool-kv-wus3 --name ci-bot-stg-client-secret --query value -o tsv
   az keyvault secret show --vault-name opstool-kv-wus3 --name ci-bot-stg-tenant-id --query value -o tsv
   ```

2. Patch the CI Vault:
   ```bash
   vault kv patch kv/selfservice/hcm-aro/aro-hcp-stg \
     client-id="<value>" \
     client-secret="<value>" \
     tenant="<value>"
   ```

There is no automated mirror between the Azure Key Vault and the CI Vault (Hashicorp Vault).

#### Migration plan for DEV Release Bot

The DEV environment currently uses the "OpenShift Release Bot" SP, created imperatively via `grant-openshift-release-bot-dev.sh`. The migration creates a **new parallel SP** rather than adopting the existing one, avoiding role-assignment adoption shims and ensuring zero downtime.

**Phase 1 — Create the new DEV bot alongside the old one:**

1. Rename `ci.dev.bot.applicationName` to `"OpenShift Release Bot - DEV"`.
2. Add a `dev` entry to `ci-bot-identity.tmpl.bicepparam`.
3. Add a `dev` entry to `ci-bot-rbac.tmpl.bicepparam` with all DEV E2E and infrastructure subscriptions.
4. Add a `dev` entry to the `ci-bot-secret` Shell step variables.
5. Deploy. Both old and new SPs coexist with identical RBAC on all DEV subscriptions.

**Phase 2 — Switch CI Vault and validate:**

6. Retrieve new credentials from Azure Key Vault (`ci-bot-dev-{client-id,client-secret,tenant-id}`).
7. Patch `kv/selfservice/hcm-aro/aro-hcp-dev` in CI Vault with the new credentials.
8. Run DEV E2E jobs and verify they authenticate and create resources without errors.

**Phase 3 — Decommission the old SP:**

9. Delete the old "OpenShift Release Bot" Entra application (`az ad app delete --id <old-app-id>`). This also removes the SP and all its role assignments.
10. Remove `grant-openshift-release-bot-dev.sh` and its references from the onboarding docs.

## What It Does Not Manage

The current `dev-ci` topology intentionally does not own several adjacent pieces of CI:

- Prow jobs, ci-operator configuration, step-registry workflows, and Boskos inventory remain in `openshift/release`.
- The on-demand DEV RP footprint created during local E2E jobs is still provisioned by the release-side workflow, not by `topology-dev-ci.yaml`.
- Static consumer artifacts such as `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` are still generated separately.
- The full lifecycle of the pooled MSI mock service principals is not yet fully declarative.

For the runtime lease model itself, see [CI Identity Leasing](identity-leasing.md).

## The Current Mixed-Management Boundary

The sharpest mixed-management boundary today is the DEV MSI mock service-principal pool used by local E2E jobs.

- `Microsoft.Azure.ARO.HCP.DevCI.E2ESubscriptionRBAC` owns the customer-subscription RBAC side for the pooled principals, using principal IDs from `config/config-dev-ci.yaml`.
- `make create-msi-mock-pool` is still a hybrid operator path:
  - `dev-infrastructure/templates/mock-identity-pool.bicep` ensures the Key Vault certificate footprint.
  - `dev-infrastructure/scripts/create-sp-for-rbac.sh` and `dev-infrastructure/Makefile` still create or update the Entra app and service principal objects and apply the home-subscription grants.
- `make populate-msi-mock-pool` still performs live Entra lookups and writes the static `dev-infrastructure/openshift-ci/msi-mock-pool.yaml` catalog that release-side jobs consume.
- `openshift/release` still owns the Boskos inventory and lease contract for the `aro-hcp-msi-mock-cs-sp-dev` resource type.

That means pool changes still span multiple control planes today: the `dev-ci` topology, local operator scripts, and release-side CI configuration.

## Long-Term Direction

The intended end state is to replace this mixed model with a single declarative producer and generated consumer artifacts:

- the pool definition would live in one canonical source of truth
- the rollout would own the pool lifecycle end to end
- downstream consumer artifacts such as Boskos inventory and the static pool catalog would be generated from that source instead of being updated separately

That is not the current behavior on this branch. Until that migration is designed and validated carefully, the mixed model above remains the supported operating model.

## Operator Entry Points

Useful local entry points for the current topology:

```bash
make dev-ci-local-run
make dev-ci-e2e-subscription-rbac-local-run
```

Use the first command for the full standalone `dev-ci` entrypoint and the second when only the customer-subscription RBAC rollout needs to be reconciled.

## Where To Look

When you need to change or debug the standalone `dev-ci` topology, start here:

- `config/config-dev-ci.yaml`
- `topology-dev-ci.yaml`
- `dev-infrastructure/dev-ci/cluster/opstool-aks-pipeline.yaml`
- `dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`
- `dev-infrastructure/configurations/e2e-subscription-rbac-assignments.tmpl.bicepparam`
- `dev-infrastructure/Makefile`
- `dev-infrastructure/openshift-ci/populate-msi-mock-pool.sh`
- [CI Identity Leasing](identity-leasing.md)
- [CI Quota Monitoring](quota-monitoring.md)
- [Opstool Cluster Guide](../ops/opstool-cluster-guide.md)

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [DEV E2E Subscription Onboarding](dev-e2e-subscription-onboarding.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Operations](operations.md)
