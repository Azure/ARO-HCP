# Shared DEV Azure Monitor Workspace Pools

`Microsoft.Azure.ARO.HCP.DevCI.AMWPool` is a child of the persistent
`DevCI.Unprivileged` entrypoint. It provisions only Azure Monitor workspaces
(AMWs), using the existing monitor module. Azure creates each AMW's managed
resource group and default ingestion resources. Job-specific DCRs, DCEs,
associations and rules remain owned by the consuming job, not this pipeline.

## Inventory

`ci.dev.amwPool` in `config/config-dev-ci.yaml` is the source of truth:

- Subscription: `0ef1ad54-9296-44cd-9600-5dc8e9a74034` (alternate DEV infrastructure).
- Deployment subscription name: `ARO HCP E2E Infrastructure (EA Subscription)`;
  keep `subscriptionName` and the catalog's `subscriptionId` aligned.
- Resource group: `aro-hcp-ci-amw-pool-shared-resources`.
- Workspace region: `westus2`, explicitly passed to the monitor module rather
  than inferred from the RG location or the consumer's region.
- Services: `services-ci-pool-0`, tagged `aroHCPPurpose=services`.
- Hosted control planes: `hcps-ci-pool-0`, tagged `aroHCPPurpose=hcps`.

The two `size` values are independent. Members are not pairs and a consumer
must not assume that equal indices belong together. A personal environment
in `westus3` in subscription `1d3378d3-5a3f-4712-85a1-2485495dfc4b` can use
these workspaces to test both cross-region and cross-subscription ingestion.
No ingestion limit or metrics-container resource overrides are introduced.

Grafana integration is deliberately omitted initially: the standalone DEV-CI
config has no shared Grafana resource ID, and the opstool workspace is not a
Grafana instance. Purpose tags leave these AMWs discoverable for future shared
Grafana integration without granting new cross-resource permissions now.

## Provisioning

The RG's `-shared-resources` suffix is protected by the existing deployed
cleanup-sweeper `skip-shared-resources` rule. No new exclusion or runtime
policy rollout is required. Keep that suffix: `persist=true` alone is
insufficient because the generic rule deletes arbitrary persistent RGs after
15 days. The `skip-managed-resource-groups` rule protects Azure's AMW-managed
RGs while their `managedBy` workspace exists; deleting the pool RG would
remove that protection as well.

From the repository root, create or reconcile only the pool:

```bash
AZURE_CONFIG_DIR="${HOME}/.azure-redhat" make -C dev-infrastructure amw-pool
```

The target selects the DEV-CI config and topology, disables the step cache,
and forces `PERSIST=true`. The equivalent templatize command is:

```bash
AZURE_CONFIG_DIR="${HOME}/.azure-redhat" \
  ./tooling/templatize/templatize-$(uname -m) entrypoint run \
  --config-file config/config-dev-ci.yaml \
  --topology-config topology-dev-ci.yaml \
  --dev-settings-file tooling/templatize/settings.yaml \
  --dev-environment dev-ci \
  --service-group Microsoft.Azure.ARO.HCP.DevCI.AMWPool \
  --persist-tag=true \
  --step-cache-dir=""
```

The full parent invocation must also pass `PERSIST=true` (for example,
`make dev-ci-local-run PERSIST=true`). This pipeline uses the existing
OpenShift Release Bot's inherited subscription Contributor permission.
It creates no role definitions or assignments and does not need to duplicate
the bot's existing constrained RBAC Administrator grant. Job identities keep
using the existing DEV-CI shared identity machinery.

Increasing either count adds only that purpose's new indexed workspaces.
Incremental ARM deployments do not delete omitted members when a count is
reduced. Drain consumers and retire members explicitly before reducing or
renaming inventory; never delete the shared pool during job cleanup.

## Catalog Export

```bash
make -C dev-infrastructure populate-amw-pool
```

This offline generator follows the identity-pool export pattern and writes
`dev-infrastructure/openshift-ci/amw-pool.yaml`. Each `amwPool` key is one
workspace name, mapped to `workspaceId`, `location`, and `purpose`. It derives
ARM IDs from the configured subscription and RG, not from a live Azure query,
so export is not proof of provisioning. Deploy and verify the workspaces
before publishing the catalog to consumers. `CONFIG_FILE` and `OUTPUT_FILE`
can be supplied directly to the script for isolated checks.

The companion release configuration registers two independent Boskos types:
`aro-hcp-services-amw-dev` and `aro-hcp-hcps-amw-dev`. Each ephemeral workflow
requests one member of each type through ordinary workflow-level leases.
The resulting `SVC_AMW_LEASE` and `HCP_AMW_LEASE` names are resolved through this
catalog by the provisioning helper. See `hack/ci/README.md` for validation and
baseline/upgrade behavior.

Merge and publish this repository's consumer support before enabling those
workflow leases. Baseline source and resolved baseline artifacts must also
contain support. Drain any personal or other unleased producers before
advertising a workspace as available; do not delete the retained workspace.
With the initial one-member inventory for each purpose, consuming workflows
serialize. Pool counts should be increased explicitly as concurrency requires.
