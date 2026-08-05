# BAMI billing / meter-emission test harness

Reusable scripts + runbook to stand up ARO HCP resources (customer infra +
cluster + node pool) in a **BAMI subscription** and observe how they bill / emit
meters, then tear them down.

It reuses the self-contained bicep templates under [`demo/bicep`](../../demo/bicep),
which create their own managed identities and role assignments — no pre-created
MSI pool and no Go e2e framework required.

---

## How billing actually works (read this first)

Understanding the chain tells you why each step below is necessary:

1. **AFEC flags route your ARM traffic.** `Microsoft.RedHatOpenShift/INT-APPROVED`
   tells Azure ARM to route this subscription's `Microsoft.RedHatOpenShift` API
   calls to the **INT RP** (uksouth, `azure-test.net`). Without it, calls never
   reach the INT RP.
2. **Registering the provider creates the subscription doc.** When you register
   the `Microsoft.RedHatOpenShift` provider, ARM sends a subscription lifecycle
   notification (`PUT /subscriptions/{id}`) to the INT RP. The frontend's
   `ArmSubscriptionPut` handler writes a **subscription document** into Cosmos.
   This is *not* a billing doc.
3. **Billing docs are per-cluster.** Only when you create an HCP cluster and it
   reaches `ProvisioningState = Succeeded` does the backend `createBillingDoc`
   controller write a `BillingDocument` (creationTime, location, tenant, managed
   RG). That doc's creation/deletion timestamps are the billable window.

So: **AFEC flags + provider registration ≠ billing.** You must actually create a
cluster that reaches `Succeeded`. See
[docs/ci/e2e-subscription-onboarding.md](../../docs/ci/e2e-subscription-onboarding.md)
for the authoritative onboarding procedure.

---

## Prerequisites

- **A BAMI subscription created through the BAMI portal.** BAMI subscriptions
  are *not* created by these scripts and cannot be created from the CLI or the
  Azure portal — you must create the subscription manually in the **BAMI portal**
  first, then use its subscription ID here:
  - PPE / INT: <https://bamiportal.microsoft-int.com>
  - Global: <https://bamiportal.microsoft.com>
  - Background: [BAMI Overview — Billing Account for Microsoft Internal](https://eng.ms/docs/products/commerce/tools/bami/overview)
- `az` CLI logged into the **tenant/cloud that subscription lives in**. For INT
  that is the `azure-test.net` ARM environment, *not* public Azure. Confirm with
  `az account show` (the sub must resolve) before running anything.
- `jq`.
- On the subscription: the `Microsoft.RedHatOpenShift` provider registered and
  the required AFEC flags `Registered` (handled by `prereqs.sh` + a Geneva
  approval — see the runbook).
- Adequate quota in the subscription (Standard DSv3 vCPUs, public IPs, role
  assignments). New subscriptions typically need quota requests filed.

---

## Full runbook (fresh subscription → billing → teardown)

### Step 0 — Create the BAMI subscription (BAMI portal, manual)

Create the BAMI subscription through the **BAMI portal** (not the Azure portal).
This is a manual, one-time action that cannot be scripted. Note its
**subscription ID** — it goes in `config.env` (see Step 2).

- PPE / INT: <https://bamiportal.microsoft-int.com>
- Global: <https://bamiportal.microsoft.com>

### Step 1 — Log in to the correct ARM cloud

The BAMI subscription lives in the **Microsoft Commerce EA Testing** tenant
(`065e9f5e-870d-4ed1-af2b-1b58092353f3`). Log in scoped to that tenant — this is
also how you switch tenants if `az` is currently pointed elsewhere:

```bash
az login --tenant 065e9f5e-870d-4ed1-af2b-1b58092353f3   # Microsoft Commerce EA Testing
az account show --subscription efc13483-cb4e-4bf2-889a-895677f5b57c -o table  # must resolve
```

If the sub does not resolve, your `az` CLI is logged into the wrong tenant/cloud
and nothing downstream will work. To list the tenants your account can reach,
run `az account tenant list -o table` (or `az account list --query '[].tenantId' -o tsv | sort -u`).

### Step 2 — Configure this harness

**Stop and think about what you are about to provision.** `setup.sh` creates
real, billable resources in the BAMI subscription. Before running anything, open
`config.env` and deliberately review every value — the target subscription and
region, and especially `NODEPOOL_SPECS` (which VM SKUs and how many of each).
Confirm it is exactly what you intend to create, and that each SKU has quota in
the region.

```bash
cd hack/bami-billing-test
cat config.env
```

All `az` calls are pinned to `BAMI_SUBSCRIPTION_ID`, so you never need
`az account set` and cannot accidentally deploy into the wrong subscription.

### Step 3 — Register providers + initiate AFEC flags

```bash
./prereqs.sh
```

This runs `az provider register` for the required providers and `az feature
register` for the AFEC flags (putting them in `Registering`/`Pending`).

### Step 4 — Approve the AFEC flags (Geneva Action, manual)

`az feature register` only *initiates* AFEC registration. A Microsoft
**PlatformServiceAdministrator** must approve it — this step **cannot be
scripted**:

1. Request JIT elevation for the `PlatformServiceAdministrator` role. In the JIT
   request form, set:
   - **Operations Category:** `Service Dev Operations`
   - **Resource Type:** `ACIS`
   - **Instance:** `Production`
   - **Scope:** `ARO`
   - **AccessLevel:** `PlatformServiceAdministrator`
   - **Justification:** a short reason (e.g. "Approve AFEC flags for BAMI billing test sub").
   - `Work Item Id` / `SafeFly Id` can be left blank unless your process requires them.
   Click **Validate Resource**, then **Submit**, and wait for the grant.
2. Once elevated, in Geneva Actions → Azure Resource Manager → Feature
   Management → **Approve Feature Registration**:
   - Namespace: `Microsoft.RedHatOpenShift`
   - Feature Names: `INT-APPROVED`
   - Subscription: your BAMI sub
3. Re-register the provider so the flags take effect:
   ```bash
   az provider register --namespace Microsoft.RedHatOpenShift --subscription <sub-id>
   ```
4. Verify all flags show `Registered`:
   ```bash
   az feature list --namespace Microsoft.RedHatOpenShift --subscription <sub-id> -o table
   ```

### Step 5 — Confirm the subscription doc was created

Once the provider is registered with the routing flag, ARM notifies the INT RP
and the frontend writes the subscription doc. Confirm via the **frontend logs**
(Kusto / logging pipeline) — look for:

```
msg="created document for subscription <sub-id>"
```

For INT, query the `ServiceLogs` database on the `hcp-int-uk.uksouth` Kusto
cluster ([ADX Web](https://dataexplorer.azure.com/clusters/hcp-int-uk.uksouth/databases/ServiceLogs)):

```kusto
frontendLogs
| where timestamp > ago(8h)
| where log contains "created document for subscription"
| take 10
```

Narrow to your subscription by adding
`| where log contains "efc13483-cb4e-4bf2-889a-895677f5b57c"`. A returned row
means the frontend created the subscription doc.

There is no Geneva Action that returns the subscription doc; logs (or SRE-level
Cosmos access) are the only way to see it.

### Step 6 — Create the cluster + node pool

**First, consider your quota.** Node pool VMs count against your subscription's
per-family and total regional vCPU quota in the region. Start by looking at what
you've configured — `NODEPOOL_SPECS` in `config.env` lists the SKUs and counts
you're about to create:

```bash
grep NODEPOOL_SPECS config.env    # or: cat config.env
```

Then check what quota you have and compare it to what those specs will create
(each VM's vCPU count × replicas, per family):

```bash
az vm list-usage --location uksouth \
  --subscription efc13483-cb4e-4bf2-889a-895677f5b57c \
  --query "[?contains(localName,'ESv5') || contains(localName,'DSv5') || contains(localName,'Total Regional')].{Name:localName, Used:currentValue, Limit:limit}" \
  -o table
```

For each family you're deploying, confirm `Limit − Used` is at least the vCPUs
you're about to add (e.g. `Standard_E4s_v5` = 4 vCPU in the `ESv5` family), and
that `Total Regional vCPUs` has room for the sum. Adjust the `--query` filter to
match your SKUs' families. If a family shows `Limit: 0` or too little headroom,
request a quota increase (portal Quotas blade) before deploying.

Then deploy:

```bash
./setup.sh        # re-verifies AFEC flags are Registered, then deploys
```

This creates the resource group, customer infra (VNet, subnets, NSG, KeyVault),
the HCP cluster, and the node pool(s) from `NODEPOOL_SPECS`.

### Step 7 — Wait for `Succeeded` (this triggers the billing doc)

```bash
./show.sh         # watch properties.provisioningState until "Succeeded"
```

When the cluster reports `Succeeded`, `createBillingDoc` writes the billing doc.

### Step 8 — Verify the billing doc

Unlike the subscription doc, the billing doc **is exposed via a Geneva Action**
(it's cluster-scoped):

```
GET /admin/v1/hcp/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{cluster}/billingdump
```

You can also confirm via backend logs (`created billing document for cluster`).

### Step 9 — Tear down

```bash
./teardown.sh     # deletes the cluster (stamps deletionTime), then the customer RG
```

Deleting the cluster stamps `deletionTime` on the billing doc, closing the
billable window.

---

## Prior art this is based on

- [`demo/`](../../demo) — self-contained az-cli + bicep cluster deploy (the base for this).
- [`test/e2e-setup/bicep`](../../test/e2e-setup/bicep) — the richer e2e setup with
  multiple cluster shapes; more powerful but coupled to the Go test harness and an
  MSI pool. Use that if you later need many cluster configurations.

## Files

| File | Purpose |
|------|---------|
| `config.env` | **Committed** target: `BAMI_SUBSCRIPTION_ID`, region, providers, and AFEC flags. The tracked record of what we run. |
| `lib.sh` | Shared config loading, subscription pinning, provider/location/AFEC checks. |
| `prereqs.sh` | One-time: registers providers and initiates AFEC flags. |
| `setup.sh` | Creates RG, customer infra, cluster, node pool (after verifying AFEC flags). |
| `bicep/nodepool.bicep` | Local, parameterized node pool template (VM size / replicas / disk) for SKU billing tests. |
| `show.sh` | Prints config + cluster/node-pool provisioning state. |
| `teardown.sh` | Deletes the cluster then the customer resource group. |

## Notes

- **INT is a real shared environment.** Clusters you create here consume real INT
  capacity and are visible to the INT RP — clean up with `teardown.sh` when done.
- `CLUSTER_VERSION` / `NODEPOOL_VERSION` default to recent values but are not
  validated up front; a stale version surfaces as an ARM deployment error. Bump
  them in config if a deploy fails on version.
- `PRIVATE_KEYVAULT` defaults to `false` to avoid private-link/DNS setup for a
  throwaway billing test; set `true` to mirror the private-KV customer shape.
- **Testing different VM SKUs.** A node pool is single-SKU, so to bill multiple
  SKUs list them in `NODEPOOL_SPECS` as space-separated `vmSize:replicas` entries
  — each becomes its own node pool on the cluster, named after the SKU
  (`Standard_D8s_v3` → `np-d8s-v3`). Example:
  `NODEPOOL_SPECS="Standard_D8s_v3:2 Standard_D16s_v3:1" ./setup.sh`.
  `NODEPOOL_OSDISK_GIB`/`NODEPOOL_OSDISK_TYPE` apply to all pools. `vmSize` is
  immutable on an existing node pool, so change SKUs by adding a new spec or
  `./teardown.sh` + redeploy. Every SKU must have quota in the subscription/region.
- For STG/PROD, change `REQUIRED_AFEC_FLAGS` and `LOCATION` in config
  (STG: `STAGING-APPROVED`; PROD: `HcpPrivatePreview InProgress`).
