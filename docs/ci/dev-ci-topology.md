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
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [CI Identity Leasing](identity-leasing.md)
- [CI Operations](operations.md)
