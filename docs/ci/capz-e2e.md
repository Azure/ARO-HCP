# CAPZ E2E Jobs

The `capz-e2e` variant of the ARO-HCP ci-operator configuration (`Azure-ARO-HCP-main__capz-e2e.yaml` in `openshift/release`) runs a different kind of test than the rest of ARO HCP CI. Instead of exercising the ARO-HCP RP with the Go E2E suite, it checks that Cluster API Provider Azure (CAPZ) can create, verify and delete an ARO HCP hosted cluster through the public RP API. The test logic lives in the `stolostron/cluster-api-installer` and `stolostron/capi-tests` repositories and is maintained by the CAPZ team, who are the approvers of the `capz-arohcp-*` steps in the step registry. This page describes only how those jobs borrow ARO HCP CI infrastructure, so that people changing slots, mock identities or environments know the jobs exist and what they depend on.

## What The Jobs Do

Both jobs follow the same shape. A small AKS cluster is created as the CAPI management cluster, the CAPI, CAPZ and ASO controllers are installed on it, and CAPZ manifests are generated and applied against an ARO HCP frontend. The job waits for the hosted cluster to come up, verifies its nodes and operators, deletes it and checks that its Azure resources are gone. Each of these phases is a `capz-arohcp-*` step, and the post steps always run a safety-net teardown and clean up role assignments left on the identities the cluster used.

| Job | Trigger | Target environment |
| :-- | :------ | :----------------- |
| `capz-e2e-dev` (presubmit) | Optional, run manually with `/test capz-e2e-dev` | A DEV environment provisioned by the job itself, so unmerged changes are exercised |
| `capz-e2e-production` (periodic) | Weekday schedule, reports to the CAPZ team's Slack channel | The existing PROD environment, with no ARO-HCP provisioning |
| `capz-e2e-images` (presubmit) | Only on request with `/test capz-e2e-images`; it never runs automatically | None; it only builds the service images that `capz-e2e-dev` deploys |

The `stolostron/capi-tests` repository also has its own periodic copy of the PROD flow in `openshift/release`. It stops after generating the manifests and is owned by the CAPZ team.

## Where It Touches ARO HCP CI

**Environment provisioning (DEV only).** `capz-e2e-dev` uses the same `aro-hcp-provision-environment` and `aro-hcp-deprovision-environment` steps as the E2E jobs, deploying the `ci01` environment with the images built by `capz-e2e-images`. Region selection is pinned rather than weighted, see [DEV CI Regional Load Management](dev-region-load-management.md).

**Slots (DEV only).** The job acquires a slot with the `aro-hcp-lease-acquire` step, like any other DEV E2E job (see the [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md)). The CAPZ setup step reads the slot environment file to find the customer subscription that the hosted cluster is created in, and the AKS management cluster is created in the same subscription. The hosted cluster's managed identities come from one of the slot's identity containers, the same [E2E identity](slot-assets.md#e2e-identities) assets the Go E2E jobs use. A CAPZ run therefore counts against the [capacity](e2e-capacity.md) budget like any other run. If CAPZ's own deletion of the cluster fails, role assignments can stay behind on the container's identities, which is why a post step removes everything scoped to the container's resource group.

**PROD.** The production job needs no slot. It takes a quota slice and an identity container from the PROD Boskos pools, and it creates its hosted cluster in the PROD customer subscription named in its configuration. Anything that changes those pools or that subscription affects it.

**Cleanup.** The hosted cluster's resource group is tagged so the usual sweepers can remove it if the job is aborted, see [CI Cleanup](cleanup.md). The jobs also run their own pre-test cleanup of leftovers from earlier aborted runs.

**Credentials.** The jobs read their Azure credentials from the same cluster profile secrets as the other ARO HCP jobs (`aro-hcp-dev` and `aro-hcp-prod`).

## Migrating To Slot-Managed Assets And ci02

Slot-manager was introduced for `e2e-parallel` and the other Go E2E jobs, and the CAPZ jobs only partly follow it. The steps already read the slot environment file, honour `SELECTED_CLUSTER_PROFILE_DIR` and take the identity container from `LEASED_MSI_CONTAINERS`, so someone started the work. What remains is mostly in the job configuration and in a few steps that still assume the legacy pools. The goal is that every CAPZ job takes its subscription, identity container and credentials from a slot, and that the DEV job runs on `ci02` instead of `ci01`. The `ci02` environment is the right home: it is smaller and cheaper per run (see [Service Cluster Consolidation](system-pool-only.md)), and the `dev-xs` catalog entry for `shard4` already says it exists for upgrade and CAPZ on-demand testing, with room for the CAPZ job next to the upgrade presubmit.

This section is a working checklist for the CAPZ team and should be removed once the jobs are migrated. Everything in it comes from reading `Azure-ARO-HCP-main__capz-e2e.yaml`, the `capz-arohcp-*` steps and `stolostron/capi-tests`.

### `capz-e2e-dev`

| Where | Today | Needed |
| :---- | :---- | :----- |
| `ARO_HCP_DEPLOY_ENV` and `ALLOWED_SUBSCRIPTIONS` | `ci01` with the four subscriptions of the `dev` environment's first shards, copied from `e2e-parallel` | `ci02`, and the subscription of the `dev-xs` pool (the one `e2e-parallel-inplace-upgrade` uses). Check the pool's `slot_count` leaves headroom for both jobs. |
| `leases` | `LEASED_MSI_MOCK_SP` and `LEASED_ARM_HELPER_SP` from the retired Boskos pools | Remove both. No `capz-*` step reads them, so today they only consume pool members. Boskos still defines the ARM helper pool, which is why the job does not fail on them. |
| Region | `LOCATION` and `MULTISTAGE_PARAM_OVERRIDE_LOCATION` pin `westus3`; the steps default `REGION` to `uksouth` when `LOCATION` is unset | Decide whether to keep a fixed region or move to `LOCATION_WEIGHTS` like the other `ci02` jobs. Weights only work if the steps take the region from the slot environment, which they currently do only for the identity container region (`MSI_REGION`). See [DEV CI Regional Load Management](dev-region-load-management.md). |
| `capz-arohcp-dev-setup` | Reads `infra-<env>-subscription-id` from the cluster profile secret | Confirm the `ci02` key exists, or use `INFRA_SUBSCRIPTION_ID`, which the slot environment already publishes for the environment's infrastructure subscription. |
| Network access for the management cluster | Adds an inbound rule `allow-istio-ingress-aks-mgmt` at fixed priority `1001` to the service cluster's node NSG, never removed | Fine while the environment is created and deleted per run. It will collide as soon as two runs share an environment or the job moves to a persistent one, so the CAPZ and ARO HCP teams should agree on a different way to reach the frontend. |
| `releases` stanza | Pins 4.19 integration releases, which the `main` and `e2e` configs do not have | Drop it unless something in the steps needs the payload. |

### `capz-e2e-production` and the `capi-tests` periodic

| Where | Today | Needed |
| :---- | :---- | :----- |
| Leases | Boskos `aro-hcp-prod-quota-slice` and `aro-hcp-test-msi-containers-prod`; the quota lease is not read by any step, and the PROD `e2e-parallel` jobs no longer use it | Use `aro-hcp-lease-acquire` and `aro-hcp-lease-release` as the other PROD E2E jobs do (`aro-hcp-persistent-e2e` is the reference), with `ARO_HCP_DEPLOY_ENV=prod` and the PROD E2E subscriptions in `ALLOWED_SUBSCRIPTIONS`. The slot then provides `LEASED_MSI_CONTAINERS`. |
| Subscription | `write-env` replaces the slot's `CUSTOMER_SUBSCRIPTION` with the `subscription-id` file in the profile directory whenever that file exists, so the slot's choice would be silently ignored | Remove the override for the ARO HCP jobs and rely on `SELECTED_CLUSTER_PROFILE_DIR`. |
| Credential profile | `VAULT_SECRET_PROFILE: prod`, while the step default and the other PROD E2E jobs use `prod-rh` | Confirm which tenant these jobs are meant to run in. |

The `configure-prow-mgmt` configs in `stolostron/capi-tests` run the same steps with the same two PROD leases and profile, so they need the same changes or need to be retired.

### Shared step behaviour

The `capz-arohcp-*` steps are thin wrappers around the generic `capz-test-*` ones (the commands are symlinks), which the `capi-tests` jobs also use, so changes to a script reach both families.

- **Credential sources.** `write-env` also supports `osServicePrincipal.json` from a cluster profile and a `/var/run/capz-azure-credentials` mount that overrides everything, including the subscription. No step in these jobs mounts either, so they are dead paths here; keep them only if some other job uses them.
- **Cleanup scope.** `cleanup-pre` and `teardown` run `make clean-azure` with `FORCE=1`, which in `scripts/cleanup-azure-resources.sh` finds Azure resources with a Resource Graph query on `name contains <prefix>` and no subscription scope, and deletes AD applications and service principals by display-name prefix. The prefixes come from the run's deployment state, falling back to the Make defaults (`prow-ci` here) and to `capz-tests`, and neither is unique per run. Two concurrent runs, or anything else whose name contains the prefix in a subscription the CI identity can read, can be hit. Slots make concurrent CAPZ runs in one subscription normal, so scope the cleanup to the run's resource group and its own tag, as the slot-based jobs do (see [CI Cleanup](cleanup.md)). This comes from reading the script, not from observing a deletion.
- **Role assignment cleanup.** `cleanup-msi-roleassignments` deletes every role assignment under the leased container's resource group, but it leaves any federated credentials. The slot's E2E identity admission removes leftover role assignments before the container is reused, so check whether this step is still needed for them; admission does not remove federated credentials, so an aborted CAPZ run can leave stale ones that need a separate cleanup.
- **Management cluster capacity.** The AKS management cluster is one `Standard_D4s_v5` node created per run in the slot's customer subscription. It draws on the same VM family quota as the pool's hosted clusters, which [E2E Capacity And Quotas](e2e-capacity.md) does not account for. It is tagged for the expired resource group cleanup, so a leaked cluster is removed after six hours.
- **Coupling to this repository.** `capz-arohcp-dev-setup` runs ARO HCP make targets (`svc.aks.kubeconfig.pipeline`, `frontend-grant-ingress`, `e2e-local/setup`) from the `aro-hcp-e2e-tests` image. Those are not a stable interface, so changes to them need to take this job into account until the dependency is replaced.

## When To Update This Page

Update it when the jobs start or stop using slots, change the environment they provision or target, or are renamed. Changes to what the CAPZ test phases do belong in the CAPZ repositories, not here.
