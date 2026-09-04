# DEV Mock-MSI ACL Isolation (ARO-29287)

Spike design for a non-vacuous DEV e2e signal that cluster delete still works
when the identity used for cluster operations has **no ACL**. This is not
about replicating the MSI RP, and it is not about product resource-cleanup
work (ARO-21580 is already tested in STG).

Status: prototype for review. Chosen mechanism is implemented behind a
dedicated suite that is **not** wired into the default DEV parallel job.

## Current topology

```
                    ┌─────────────────────────────────────────┐
                    │  e2e job (OTE, parallelism ~24)         │
                    │                                         │
  Boskos lease ────►│  one MSI mock SP for the whole job      │
  (LEASED_MSI_MOCK_SP)│  hardcodedIdentityManagedIdentities…   │
                    │  returns that SP for every operator UAMI│
                    │                                         │
                    │  spec A  spec B  spec C  …   (parallel) │
                    └─────────────────────────────────────────┘
                                      │
                                      ▼
                    subscription-scoped grants on that SP:
                      • custom role `dev-msi-mock-<sub>`
                      • built-in Key Vault Crypto User
```

- **Shared personal-dev identity:** `aro-dev-msi-mock2`
  (`miMockPrincipalId` `d6b62dfa-87f5-49b3-bbcb-4a687c4faa96` in
  `config/config.yaml`). Never strip this principal.
- **Job-leased pool:** Azure display name `aro-dev-msi-mock-pool-<i>`, Boskos
  / catalog key `aro-hcp-msi-mock-cs-sp-dev-<i>` in
  `dev-infrastructure/openshift-ci/msi-mock-pool.yaml`, leased per e2e **job**
  (same shape as the ARM-helper CheckAccess pool). See the
  [naming bridge](identity-leasing.md#naming-bridge).
- **Identity-container pool:** per-**spec** leasing of UAMI resource groups.
  Orthogonal. The mock SP still authenticates as every operator regardless of
  which UAMI resource IDs the cluster declared.
- **Product deny assignments:** `ClusterDenyAssignment` is **disabled** in
  DEV/INT (`clustersService.denyAssignments: disabled`) because there is no
  real FPA. E2E can list deny assignments (cleanup probes) but cannot create
  them. Product deny assignments also *except* operator principals — and in
  DEV the mock SP *is* those operators — so enabling the product controller
  would not strip mock ACL.

Deleting customer UAMIs in DEV (`cluster_delete_missing_identities` today) is
vacuous: CAPZ/Hypershift still get mock-SP credentials that keep
subscription `dev-msi-mock`.

## Isolation options considered

| Mechanism | Parallel-safe? | Restorable? | Notes |
|---|---|---|---|
| Strip the job's subscription `dev-msi-mock` grant | **No**, unless no sibling spec is running | Yes, if we recreate the same assignment GUID | Ginkgo `Serial` does **not** serialize across OTE worker processes. Default DEV suite is `Parallelism: 24`. |
| RG-scoped deny targeting the mock SP | Yes | Yes (delete deny; RG delete removes it) | Ideal spatially, but e2e identities cannot write deny assignments; FPA-backed controller is off in DEV. |
| ABAC condition on the subscription grant excluding this spec's RGs | Maybe | Fragile | Mutates the shared grant in place; abort mid-update can leave a poisoned condition. Not prototyped. |
| Per-spec mock SP in the backend | Yes | N/A | Process-wide hardcoded client. Would be "replicate MSI RP". Out of scope. |
| Dedicated suite, `Parallelism: 1`, strip the **leased pool** SP | Yes (no siblings in that invocation) | Yes | Reuses the ARM-helper pattern at **suite** grain. Chosen. |

## Chosen mechanism

1. Leave `test/e2e/cluster_delete_missing_identities.go` unchanged. It is
   already `StageAndProdOnly`, so DEV/INT suites never select it.
2. Add a **new** DEV spec in `test/e2e/cluster_delete_missing_identities_dev_mock_msi.go`,
   selected **only** by suite `development/mock-msi-acl` (`Parallelism: 1`).
   It is excluded from every existing parallel suite.
3. After the cluster is verified healthy, **delete** the leased mock SP's
   subscription-scoped `dev-msi-mock` and Key Vault Crypto User assignments,
   then delete the dedicated UAMIs (so CS still sees missing identities),
   then delete the cluster.
4. `DeferCleanup` always recreates the stripped assignments using the original
   assignment GUIDs (the Bicep names are `guid(subscription().id, principalId, roleDefinitionId)`).
5. **Do not delete the mock app registration.** Entra ghosts / directory quota.
6. **Refuse** to strip `aro-dev-msi-mock2`. Only pool principals from
   `msi-mock-pool.yaml` (or an explicit `ARO_HCP_MSI_MOCK_PRINCIPAL_ID` that
   matches a pool entry) are eligible.

This is the CheckAccess identity-leasing pattern applied to MSI mock: isolation
is "this invocation owns the leased SP exclusively", not "mutate a grant while
24 other specs are using it".

## Lease vs dedicated UAMIs

The DEV spec still uses `useMsiPool=false` / `MIContainers(0)` so it can delete
UAMIs without corrupting the identity-container pool (ARO-29288). Permission
strip is what makes the DEV signal non-vacuous; UAMI delete is extra CS
coverage, not a substitute.

If ARO-29288 concludes CS does not need identity deletion in DEV, switch this
spec to `MIContainers(1)` and stop calling `DeleteUserAssignedIdentities`.
Keep the mock-SP strip.

## Restore and abort

| Event | What restores the leased SP |
|---|---|
| Spec succeeds or fails after strip | `DeferCleanup` → `RestoreMockMSIPermissions` (same assignment GUIDs) |
| Process killed, `DeferCleanup` does not run | Next privileged `mock-identity-rbac.bicep` apply recreates the deterministic GUIDs. Until then, the next job that leases this pool member will see operators without ACL (fail closed, noisy). |
| Personal-dev / shared `aro-dev-msi-mock2` | Strip is refused. No restore problem. |

Do **not** rely on the hourly leftover sweeper: it deletes *orphaned*
assignments, it does not re-create missing ones.

Required env for the DEV spec (ARO-29290 must export these from the provision
step into the test step; today `LEASED_MSI_MOCK_SP` is consumed at provision
time and is not guaranteed in the test process):

- `LEASED_MSI_MOCK_SP` — Boskos catalog key (`aro-hcp-msi-mock-cs-sp-dev-<i>`),
  looked up in `msi-mock-pool.yaml`. This is **not** the Azure display name
  `aro-dev-msi-mock-pool-<i>`.
- `ARO_HCP_MSI_MOCK_PRINCIPAL_ID` — optional explicit principal. Prefer this
  when provision already exported it (ARO-29290). If both env vars are set,
  they must resolve to the **same** principal or the spec errors.

Optional: `ARO_HCP_MSI_MOCK_POOL_CATALOG` overrides the catalog path.

Exporting those variables is **necessary but not sufficient**. The spec
strips whichever principal the env resolves to. It cannot see Helm. If
Cluster Service / backend are still impersonating a different mock SP
(typically `aro-dev-msi-mock2`), strip is a no-op for the identity that
actually runs cluster ops and delete can still succeed — a vacuous pass.
ARO-29290 must export the **same** values Helm applied and prove live Helm
matches the lease (see [Helm must match the lease](#helm-must-match-the-lease-false-pass)).

## What this prototype does not do

- Wire `development/mock-msi-acl` into `openshift/release` (ARO-29289).
- Change the hardcoded MI dataplane client.
- Enable product deny assignments in DEV.
- Delete Entra applications.

## Helm must match the lease (false pass)

The spec's first `By` only resolves env (`LEASED_MSI_MOCK_SP` /
`ARO_HCP_MSI_MOCK_PRINCIPAL_ID`). It does **not** read Cluster Service or
backend. Personal-dev soak showed env can say pool member N while live Helm
is still `aro-dev-msi-mock2`.

| Live impersonation | Env / strip target | Create | Strip | Delete | Signal |
|---|---|---|---|---|---|
| Leased pool SP (Helm `miMock*` = lease) | Same pool SP | Succeeds | Removes the operators' ACL | Succeeds without ACL | **Real** |
| `aro-dev-msi-mock2` (default pers Helm) | Pool member N | Succeeds (mock2 still has ACL) | Removes unused pool grants | Succeeds via mock2 ACL | **Vacuous** |
| Pool client ID + `msiMockCert2` (or the reverse) | Anything | Fails `AADSTS700027`; Maestro posts no ManifestWork | Never reached | n/a | Env broken, not a spec bug |

`kubectl get deploy -o yaml` is not enough: a failed or cached Helm upgrade can
leave the **desired** spec on new `miMock*` values while Ready pods are still
the previous ReplicaSet. Confirm **running pod** args and the **mounted** mock
cert on every replica.

List role assignments with ARM `$filter=assignedTo('{principalId}')`. The Go
SDK's `principalId eq '{id}'` form returns `400 UnsupportedQuery`.

## How to run the prototype

The RP under test must already be provisioned with the **job-leased pooled**
MSI mock SP (Helm `miMock*` on **both** Cluster Service and backend), not
personal-dev `aro-dev-msi-mock2`. Pass the same Boskos catalog key that
provision consumed.

`LEASED_MSI_MOCK_SP` is the Boskos / static-catalog key
(`aro-hcp-msi-mock-cs-sp-dev-<i>`), not the Azure app display name
(`aro-dev-msi-mock-pool-<i>`). See the
[naming bridge](identity-leasing.md#naming-bridge).

If `ARO_HCP_MSI_MOCK_PRINCIPAL_ID` is also set, it must match that lease's
`principalId` in `msi-mock-pool.yaml`.

```bash
export AROHCP_ENV=development
export CUSTOMER_SUBSCRIPTION=<name>
export LOCATION=<region>
# Boskos key for the SP this RP was provisioned with — never aro-dev-msi-mock2
export LEASED_MSI_MOCK_SP=aro-hcp-msi-mock-cs-sp-dev-0
# Optional; if set, must match the lease (example is pool member 0)
# export ARO_HCP_MSI_MOCK_PRINCIPAL_ID=db27175c-5bd0-48b4-929a-41de9a53ffbf
make -C test
./test/aro-hcp-tests run-suite "development/mock-msi-acl"
```

`run-suite development/mock-msi-acl` is the CI-shaped invocation (`Parallelism`
is the literal `1`). `make e2e-local/run-test TEST_NAME="Customer should be
able to delete an HCP cluster after stripping DEV mock-MSI permissions"`
exercises the same spec through the personal-dev port-forward. This spec
does not create a node pool; do not use the 4.21 complete-cluster-create
entry as a substitute.

### Personal-dev overlay

Default pers Helm is `aro-dev-msi-mock2` (`miMockCertName: msiMockCert2`).
The spec refuses to strip that principal, so you must point **Cluster Service
and backend** at a pooled member first, run the spec, then put pers back on
mock2. `DeferCleanup` restores RBAC, not Helm `miMock*`.

Pick a high-index Boskos key so you are less likely to collide with a live CI
job. Look up `clientId` / `principalId` / `certName` in
`dev-infrastructure/openshift-ci/msi-mock-pool.yaml`. Overlay those three
fields at `.clouds.dev.environments.pers.defaults` (same shape CI uses). Pin
service image digests that match the charts you are deploying — a newer
backend chart with `--backup-schedule-cadence` against an older image
crashloops the new ReplicaSet while old pods keep serving.

```bash
export DEPLOY_ENV=pers
export OVERRIDE_CONFIG_FILE=/path/to/pers-combined-override.yaml
# PERSIST=true does not bypass .step-cache. A cached Helm digest can skip
# the upgrade while _artifacts/config.yaml already shows the overlay.
# Cluster Service and backend are separate releases / sentinels.
PERSIST=true make pipeline/ClusterService \
  OVERRIDE_CONFIG_FILE="${OVERRIDE_CONFIG_FILE}" \
  STEP_CACHE_DIR=/tmp/cs-step-cache-force-helm
PERSIST=true make pipeline/RP.Backend \
  OVERRIDE_CONFIG_FILE="${OVERRIDE_CONFIG_FILE}" \
  STEP_CACHE_DIR=/tmp/backend-step-cache-force-helm
```

After Helm reports success, confirm the **live** impersonation before
running the spec:

1. SecretProviderClass `objectName` is the pool `certName` (not
   `msiMockCert2`) in **both** `clusters-service` and `aro-hcp`.
2. Running CS and backend pods have the pool `clientId` / `principalId`.
   Desired deploy spec can lie if an old ReplicaSet is still Ready.
3. Mounted mock cert fingerprint on **every** CS replica matches the Key
   Vault certificate named in the catalog (`az keyvault certificate show
   --vault-name aro-hcp-dev-svc-kv --name <certName>`), not mock2
   (`CN=msimock.hcp.osadev.cloud`). CSI binds at pod start: Helm can update
   the SecretProviderClass in place, then the first new replica can still
   mount the previous `objectName`. Recycle any replica whose cert does not
   match.
4. Backend is not crashlooping on an unknown flag.

When finished, redeploy Cluster Service and backend **without** the pool
`miMock*` overlay (fresh `STEP_CACHE_DIR` again) so pers is back on
`aro-dev-msi-mock2`.

## Tickets

- Spike: [ARO-29287](https://issues.redhat.com/browse/ARO-29287)
- CS delete vs strip: [ARO-29288](https://issues.redhat.com/browse/ARO-29288)
- Productionize strip/restore + env export: [ARO-29290](https://issues.redhat.com/browse/ARO-29290)
- Ungate in default DEV e2e: [ARO-29289](https://issues.redhat.com/browse/ARO-29289)
