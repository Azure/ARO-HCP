# Spec: `AllAroHcpLogs` — update-policy log routing

Status: **Draft (for review)**
Date: 2026-10-19
Branch: `rewrite-kusto-routing`
Architecture & KQL reference: [`docs/logging.md`](logging.md)

This is the durable spec: *what* we are building, *which steps are required*, and
*how we know it works*. The architecture, routing diagram, and concrete KQL live
in [`docs/logging.md`](logging.md).

---

## 1. Problem & context

Today the arobit (Fluent Bit) forwarder owns all log routing: ~13 `azure_kusto`
outputs fed by ~7 `rewrite_tag`/`grep`/`modify`/`lua` filters, spraying into
tables across three ADX databases (`ServiceLogs` P90D, `HostedControlPlaneLogs`
P14D, `MonitoringEvents` P90D). Each table carries its column extraction in a
per-table ingestion mapping.

Consequences: routing is split across two languages/repos (forwarder config +
`tables/*.kql`); adding a table touches both plus Helm fixtures; many outputs mean
many on-disk buffers (a factor in the forwarder's memory footprint); and nothing
in CI validates the routing decisions.

The repo already uses the target pattern for Event-Hub data (`alertEvents`,
`aksEvents`/`kubeAudit`, `maestroMqttConnections`): a raw landing table → a
transform function → a **transactional update policy** → a typed table. This spec
generalizes that to the Fluent Bit path, in a **new database** so no existing
table is migrated in place.

## 2. Goals / non-goals

### Goals

- **G1** — Route and transform logs with ADX update policies instead of Fluent
  Bit output plugins.
- **G2** — Everything lives in one new database, **`AllAroHcpLogs`**, so the
  HCP/service split also moves into KQL (update policies can't cross databases).
- **G3** — HCP logs (namespace prefix `ocm-*`) land in a dedicated
  **`hcpContainerLogs`** table with **14-day** table-level retention; everything
  else inherits the 90-day database default.
- **G4** — Reduce the forwarder to a single `azure_kusto` output + single
  ingestion mapping.
- **G5** — "Add a new table" becomes a single `.kql` change — no forwarder edit,
  no Helm fixture churn.
- **G6** — No in-place schema/data migration of existing tables (stand up in
  parallel, repoint readers via config, decommission later).

### Non-goals

- Changing the Event-Hub tables (`alertEvents`, `aksEvents`/`kubeAudit`,
  `maestroMqttConnections`) or the CI tables (`ci*`, written by `tenant-quota`).
  They stay in their current databases unless a follow-up decides otherwise.
- Changing enrichment semantics (environment/region/cluster, k8s metadata, the
  mgmt `cluster_id`/`resource_group` annotation Lua). These stay in Fluent Bit.
- Changing Geneva/MDSD/Shoebox forwarding (a separate sink). **It is preserved,
  not untouched:** the Shoebox fork depends on the `ocm.logs` tag and the
  `cluster_id` enrichment produced in the forwarder, so `filter.namespace_router`
  and the mgmt `set_cluster_id` Lua **must be retained** even though the other
  Kusto-routing filters are deleted. See R3 and §9 risk.
- Changing the Grafana/Prometheus-based dashboards (they don't read ADX).

## 3. Requirements

- **R1** New database `AllAroHcpLogs`, DB default `softdelete = P90D`,
  `hotcache = P2D`.
- **R2** Single raw landing table `rawLogs(timestamp, tag, log:dynamic)`,
  `softdelete = 0s` (ephemeral; `1h` during bake-in), single ingestion mapping
  `rawMapping`.
- **R3** One Fluent Bit `azure_kusto` output → `AllAroHcpLogs.rawLogs` (matching
  both `kubernetes.*` and `ocm.logs`, not `shoeboxlogs`); the **Kusto-routing**
  `rewrite_tag`/`grep` filters removed (`container_name_router`, snapshot routers,
  `drop_kube_applier_snapshots`); enrichment filters retained. **`filter.namespace_router`
  (`ocm-*` → `ocm.logs`) and the mgmt `set_cluster_id` Lua are retained** — the
  Geneva/Shoebox fork (`filter-shoebox.conf`/`output-shoebox.conf`, mgmt+mdsd)
  depends on them and is left intact.
- **R4** Update policies (all `IsTransactional: true`) fan `rawLogs` out to:
  `hcpContainerLogs`, `frontendLogs`, `backendLogs`, `clustersServiceLogs`,
  `fleetLogs`, `kubeApplierLogs`, `kubernetesEvents`, `cosmosResourceSnapshots`,
  `kubernetesResourceSnapshots`, `systemdLogs`, `azureVnetLogs`, and a catch-all
  `containerLogs`.
- **R5** `hcpContainerLogs` filter is `namespace_name startswith "ocm-"`;
  table-level `softdelete = 14d`.
- **R6** Each log lands in exactly one table (catch-all uses explicit negative
  filters; routing prefixes have one source of truth).
- **R7** Target table schemas (columns, order, types, names) identical to today's
  equivalents, except the new `hcpContainerLogs`, so readers migrate by config.
- **R8** CI validates the new KQL (DDL + policy creation) via the Kusto emulator.
- **R9** No `.alter` on any existing production table during this change.

## 4. Design summary

```
Fluent Bit (enrich only, 1 output) ──► AllAroHcpLogs.rawLogs (ephemeral)
                                            │ transactional update policies
   ┌────────────────────────────────────────┼────────────────────────────────┐
 hcpContainerLogs(14d)        frontendLogs / backendLogs / ...        containerLogs
 namespace ocm-*              by container_name prefix                (catch-all: non-ocm,
                             cosmos/kubernetesResourceSnapshots        non-service, non-snapshot)
                             (by snapshotType)
                             systemdLogs / azureVnetLogs (by tag)
```

Each target table's `.kql` contains: `.create-merge table` (unchanged schema) +
`.create-or-alter function rawToX()` (the old ingestion-mapping paths become
`project` lines) + `.alter table X policy update [{Source: rawLogs, Query:
rawToX(), IsTransactional: true}]`. Full KQL in [`docs/logging.md`](logging.md).

Routing source of truth: `KnownServiceContainerPrefixes()` /
`IsKnownServiceLog()` helper functions, used by the per-service positive filters
and the catch-all negative filter.

## 5. Key decisions & rationale

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | New DB, not in-place migration | Zero risk to live tables; parallel verify; clean rollback (G6/R9). |
| D2 | One DB for everything | Update policies can't cross DBs; one DB lets KQL own the HCP/service split, so the forwarder needs **zero** routing. |
| D3 | Per-table retention, not per-DB | `hcpContainerLogs` 14d via table-level policy; removes the need for a separate HCP database. |
| D4 | Single `rawLogs` table + single output | Maximal forwarder simplification; `dynamic` column holds any shape (container/systemd/vnet). |
| D5 | `IsTransactional: true` + `softdelete=0s` | MS-recommended for production; 0s source requires transactional. Bake-in uses `1h` for a safety net. |
| D6 | Keep target schemas identical | Reader migration becomes config-only; shrinks blast radius (R7). |
| D7 | New `.kql` in a subdir, separate validation DB | Avoids table-name collisions with the legacy flat `tables/` set in the validator's single DB (see §7). |

## 6. Implementation plan

Steps are grouped by phase. **[REQUIRED]** = needed for a correct parallel
deployment; **[CUTOVER]** = needed to make `AllAroHcpLogs` the source of truth;
**[LATER]** = cleanup/decommission.

### Phase 0 — Stand up `AllAroHcpLogs` (additive, dual-write, dev only)

1. **[REQUIRED] Config.** Add `kusto.allAroHcpLogsDatabase: "AllAroHcpLogs"` to
   `config/config.yaml` (base + dev defaults + `config.msft.clouds-overlay.yaml`
   for int/stg/prod), and wire `forwarder.kusto.allAroHcpLogsDatabase` in
   `observability/arobit/values-{svc,mgmt}.yaml`. Update `config.schema.json`.
   `cd config && make materialize`.
2. **[REQUIRED] KQL files** under
   `dev-infrastructure/modules/logs/kusto/tables/allarohcplogs/`:
   `00_rawLogs.kql`, `01_serviceRouting.kql`, `hcpContainerLogs.kql`,
   `frontendLogs.kql`, `backendLogs.kql`, `clustersServiceLogs.kql`,
   `fleetLogs.kql`, `kubeApplierLogs.kql`, `kubernetesEvents.kql`,
   `containerLogs.kql`, `systemdLogs.kql`, `azureVnetLogs.kql`,
   `cosmosResourceSnapshots.kql`, `kubernetesResourceSnapshots.kql`.
   Numeric prefixes on `00_`/`01_` guarantee the raw table + routing helpers are
   created before the policies that reference them (the validator sorts files).
3. **[REQUIRED] Bicep.** In `modules/logs/kusto/main.bicep`: create the 4th
   database (`database.bicep`, P90D/P2D), `loadTextContent` the new table set in
   deterministic order, and emit one `script.bicep` resource per file. Ensure the
   final permission-strip `dummyScript` runs for the new DB too.
4. **[REQUIRED] Grants.** Forwarder cluster-log MSI → `Ingestor` on
   `AllAroHcpLogs`; admin-api / exporter / Grafana readers → `Viewer`
   (`templates/kusto-grant-ingest.bicep`, `modules/logs/kusto/grant-access.bicep`,
   params in `configurations/kusto-{svc,mgmt}-ingest.tmpl.bicepparam`). Add the
   entity group `AllAllAroHcpLogs:AllAroHcpLogs` to the `entity-groups` step in
   `geography-pipeline.yaml`.
5. **[REQUIRED] Emulator validation** (see §7): extend `hack/kql-verify.sh` to run
   a **second** `kustoctl validate kql` against the `allarohcplogs/` subdir using a
   **fresh validation database**, OR extend `tooling/kustoctl` to validate a
   subdir into its own DB. This is required because the current validator globs a
   single dir into a single DB and our table names collide with the legacy set.
6. **[REQUIRED] Forwarder dual-write.** In
   `observability/arobit/deploy/templates/forwarder-configmap.yaml` add the
   `rawLogs` output **alongside** existing outputs (keep enrichment filters, and
   keep `filter.namespace_router` + the mgmt `set_cluster_id` Lua +
   `filter-shoebox.conf`/`output-shoebox.conf` intact — see §9 Geneva/Shoebox risk).
   When the legacy outputs are later removed (Phase 3), delete only the
   Kusto-routing filters, never the Shoebox fork. Regenerate the
   `zz_fixture_*arobit*` Helm fixtures and
   `dev-infrastructure/zz_fixture_TestHelmTemplate_dev_westus3_{svc,mgmt}_1_arobit.yaml`.
7. **[REQUIRED] Add the routing test** (new, hermetic — see §7).

### Phase 1 — Verify in parallel (dev)

8. **[REQUIRED]** Deploy to dev; confirm `rawLogs` ingests and each target table
   fills. Compare per-table `kusto_logs_age_in_seconds` and row counts between
   legacy tables and `AllAroHcpLogs`. Watch
   `.show ingestion failures | where OriginatesFromUpdatePolicy == true`.
9. **[CUTOVER]** Repoint dev readers (see Phase 2 list) at `AllAroHcpLogs` and run
   `test/e2e/kusto_logs_present.go` + `exporter_metrics.go` against dev.

### Phase 2 — Cutover readers (per environment)

10. **[CUTOVER]** hcpctl: `pkg/kusto/templates/{builtin,custom}/queries.yaml`
    `database:` values → `AllAroHcpLogs`; `pkg/snapshot` DB template vars
    (`gather_enriched.go`); `pkg/mustgather` `ServicesTables` + HCP path →
    `hcpContainerLogs`. Update the affected unit-test assertions and regenerate
    `testdata/zz_fixture_*` goldens.
11. **[CUTOVER]** `tooling/aro-hcp-exporter` kusto DB config.
12. **[CUTOVER]** `test/util/config` + `test/util/verifiers/{kusto,swift,
    must_gather_cli}.go` — DB name + HCP table name (`containerLogs` →
    `hcpContainerLogs`). `test/util/cleanup/kusto-role-assignments` — add
    `AllAroHcpLogs` to its DB list.

### Phase 3 — Retire the old path

13. **[CUTOVER]** Remove the legacy `azure_kusto` outputs + all `rewrite_tag`
    rules from the forwarder; regenerate fixtures. `AllAroHcpLogs` is now the only
    sink. Tighten `rawLogs` retention to `0s`.
14. **[CUTOVER]** Promote dev → int → stg → prod via config overlay +
    sdp-pipelines (prod via ADO/EV2, `aka.ms/arohcp-pipelines`).

### Phase 4 — Decommission

15. **[LATER]** Once nothing reads `ServiceLogs`/`HostedControlPlaneLogs`, remove
    their tables/grants (and the databases if fully unused) via
    `cleanup/delete.kusto.instance.pipeline.yaml`.

---

## 7. Testing strategy

Two questions, answered below: **(A)** what do existing tests already cover, and
**(B)** what must we add. Short version: existing tests give us strong *creation*
and *end-to-end presence* coverage but **zero data-level routing coverage** — the
one thing this change actually introduces — so one new hermetic test is required.

### 7.1 Existing tests — inventory and verdict

| Test / gate | What it covers | Verdict for this change |
|---|---|---|
| `make verify-kql` (`hack/kql-verify.sh` → `kustoctl validate kql`) | Applies each `.kql` to a real Kusto emulator via `.execute database script`; fails on `Result==Failed`. Update-policy **creation** validates the function's output schema against the target table (types + order). | **EXTEND.** Globs one dir into one DB; our new table names collide with the legacy flat set → add a second invocation against the `allarohcplogs/` subdir in a **fresh** validation DB. After that, it validates all new DDL + policy schema compatibility. Does **not** ingest, so proves nothing about routing values. |
| arobit Helm fixtures (`TestHelmTemplate`, `zz_fixture_*arobit*`, the two `dev-infrastructure/zz_fixture_*arobit.yaml`) | Golden-compares the rendered forwarder config. | **REGENERATE.** The golden diff inherently captures "one output, no rewrite_tag". No new test logic needed; optionally add a focused assertion that exactly one `azure_kusto`/`rawLogs` output exists. |
| `tooling/hcpctl/pkg/kusto/query_test.go` | Asserts exact generated KQL per definition incl. `database('ServiceLogs')` / table names. | **UPDATE** at cutover (Phase 2): DB string → `AllAroHcpLogs`, HCP table → `hcpContainerLogs`. Pure assertion churn; no semantic change. |
| `tooling/hcpctl/pkg/mustgather/*_test.go` | `ServicesTables`, `NormalizedLogLine` kusto tags, query gen. | **UPDATE** at cutover: HCP table rename; `NormalizedLogLine` column tags unchanged (R7). |
| `tooling/hcpctl/pkg/snapshot` goldens (`testdata/zz_fixture_*`) | Rendered snapshot queries. | **REGENERATE** at cutover (DB vars changed). |
| `tooling/tenant-quota/.../writer_integration_test.go` | Loads real `ci*.kql` into an emulator, ingests, asserts. | **RELY AS-IS.** CI tables out of scope. This is also the precedent for the new routing test (§7.2). |
| `tooling/e2e-cost/kusto_test.go` | Exact `kubeAudit` CSL string. | **RELY AS-IS.** `kubeAudit` out of scope (unless folded in later). |
| `test/e2e/kusto_logs_present.go` (+ `test/util/verifiers/{kusto,swift,must_gather_cli}.go`) | **Real pipeline**: create HCP cluster → fluentbit → ADX → query. Asserts presence of frontend/backend/clusters-service (service), `ocm-arohcp/.../kube-apiserver` (HCP), and swift-router checks joining `cosmos`/`kubernetesResourceSnapshots`/`containerLogs`. | **UPDATE + RELY.** Repoint verifiers to `AllAroHcpLogs` + `hcpContainerLogs`. This is the **end-to-end safety net**: a green run proves update-policy routing works for real for the service tables, the HCP table, and the snapshot tables. Keep it required in the dev gate before promotion. |
| `test/e2e/exporter_metrics.go` + `tooling/aro-hcp-exporter/...kusto_logs_current_test.go` | `kusto_logs_age_in_seconds` present & per-table. | **RELY.** Also the operational **canary** for each cutover. |

### 7.2 New test required: hermetic routing/transform test

**Why it's required.** Nothing above data-tests the transforms. `verify-kql`
proves the policy *can be created* (schema-compatible) but not that
`rawToFrontendLogs()` actually selects frontend rows and maps `log.msg → msg`.
`kusto_logs_present.go` proves presence end-to-end but is slow (minutes, creates a
real cluster), runs only in dev, and only checks a handful of sources — it will
not catch, say, `cosmosResourceSnapshots.resourceID` being mapped from the wrong
path, or a service log leaking into the catch-all. Routing correctness is exactly
what this change moves into KQL, so it needs a fast, hermetic, field-level test.

**Shape** (mirror `tenant-quota/writer_integration_test.go`): against the Kusto
emulator (gated on an endpoint env var, same as `verify-kql`),

1. Apply the `allarohcplogs/*.kql` files in order into a fresh DB.
2. `.ingest inline`/`.set-or-append` a curated set of `rawLogs` fixture rows: one
   per source type — an `ocm-*` HCP line, each service container, a
   `snapshotType=cosmos` row, a `snapshotType=kubernetes` row, a systemd row, an
   azure-vnet row, and an "unknown" container line.
3. Assert each row landed in **exactly one** expected table with the **expected
   column values** (and that known-service lines are absent from `containerLogs`).
4. Assert `hcpContainerLogs` retention is `14d`.

**Precedent/placement.** Reuse the emulator harness from `hack/kql-verify.sh`;
implement as a Go test under `dev-infrastructure/modules/logs/kusto/` or as a
`kustoctl validate routing` subcommand so it runs in the same CI job that already
spins the emulator. Keep it behind the emulator-endpoint env gate so it's skipped
when the emulator isn't available (like the existing integration tests).

**Open risk to resolve first (spike).** Confirm `kustainer-linux` actually
**fires** update policies on `.ingest inline`/`.set-or-append` (it is known to
*create* them — `verify-kql` already applies the existing `alertEvents` policy).
If the emulator does not execute policies on ingest, the hermetic data test is not
possible and we must lean harder on `kusto_logs_present.go` (extend it to assert
each of the 12 tables, accepting slower feedback). **This spike is the first task
of Phase 0** because it determines the whole test approach.

### 7.3 Coverage matrix

| Property to guarantee | Covered by |
|---|---|
| New KQL is syntactically valid; policies are schema-compatible | `verify-kql` (extended) — CI, fast |
| Each source type routes to the correct table, with correct field values | **New routing test** (§7.2) — CI, fast (pending spike) |
| Exactly-one-table (no catch-all leakage/duplication) | **New routing test** |
| `hcpContainerLogs` retention = 14d | **New routing test** (`.show table ... policy retention`) |
| Forwarder emits exactly one `rawLogs` output, no rewrite_tag | arobit Helm fixtures (regenerated) |
| Real logs flow fluentbit→ADX→queryable for service/HCP/snapshot | `kusto_logs_present.go` (repointed) — dev gate |
| Freshness / no ingestion stall post-cutover | exporter `kusto_logs_age_in_seconds` — canary |
| Readers return correct results against new DB | hcpctl unit tests (updated) + `must_gather_cli`/`swift` verifiers |

## 8. Rollout & rollback

Rollout: Phase 0–1 in dev (dual-write, verify) → Phase 2 cutover readers per env
→ Phase 3 drop legacy outputs + promote int/stg/prod → Phase 4 decommission.
Prod changes go through the ADO/EV2 + sdp-pipelines path.

Rollback (any phase before decommission): re-enable the legacy `azure_kusto`
outputs / `rewrite_tag` rules in the forwarder and revert reader config. The
`AllAroHcpLogs` DB is inert when no data reaches `rawLogs`; leaving it in place
costs only idle storage.

## 9. Risks & mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Bad policy function blocks **all** ingestion (single `rawLogs` funnel, transactional) | Total log loss | `verify-kql` + routing test gate every change; bake-in `softdelete=1h` so failures are recoverable and visible via `.show ingestion failures`; dev-first rollout. |
| Fan-out multiplier (~12 policies/extent) too costly at prod volume | ADX cost/latency | Measure in Phase 1; escape hatch: pull `systemd`/`azureVnet` to direct outputs, or split `rawLogs` into coarse raw tables. |
| kustainer doesn't fire update policies on ingest | No hermetic data test | §7.2 spike first; fall back to extended `kusto_logs_present.go`. |
| Validator name collision / load order | CI false pass/fail | Separate validation DB for subdir + numeric prefixes (D7, step 5). |
| Reader repoint missed somewhere | Stale/empty query results | Blast-radius checklist (§10); exporter canary; keep dual-write until all readers confirmed. |
| Enrichment field drift (e.g. `log.snapshotType`, annotation Lua) | Mis-routing | Routing test fixtures derived from real forwarder output; no change to enrichment in this spec. |
| **Deleting `namespace_router` breaks Geneva/Shoebox** (the `ocm.logs` fork depends on it) | **High** — customer Shoebox logs silently stop reaching Geneva; only manifests on mgmt+mdsd, so dev/e2e may miss it | Retain `filter.namespace_router` + the mgmt `set_cluster_id` Lua + `filter-shoebox.conf`/`output-shoebox.conf` unchanged; have `rawLogs` match `ocm.logs` too (so HCP logs still reach Kusto); add a **Helm-fixture assertion on a mgmt+mdsd render** that `filter-shoebox`/`output-shoebox` render and the `forward` to port `5002` exists. |

## 10. Blast-radius checklist (file-touch)

Infra/producers: `config/config.yaml`, `config/config.msft.clouds-overlay.yaml`,
`config/config.schema.json`; `dev-infrastructure/modules/logs/kusto/main.bicep` +
`tables/allarohcplogs/*.kql`; `templates/kusto-grant-ingest.bicep`,
`modules/logs/kusto/grant-access.bicep`, `configurations/kusto-*-ingest.tmpl.bicepparam`;
`geography-pipeline.yaml` (entity groups); `hack/kql-verify.sh` (+ maybe
`tooling/kustoctl`); `observability/arobit/deploy/templates/forwarder-configmap.yaml`,
`values-{svc,mgmt}.yaml`, and all `zz_fixture_*arobit*`.

Readers (cutover): `tooling/hcpctl/pkg/kusto/templates/{builtin,custom}/queries.yaml`,
`pkg/snapshot/gather_enriched.go` (+ query goldens), `pkg/mustgather/{queries,gather}.go`
(+ tests); `tooling/aro-hcp-exporter` kusto config; `test/util/config`,
`test/util/verifiers/{kusto,swift,must_gather_cli}.go`,
`test/util/cleanup/kusto-role-assignments`.

Out of scope (confirm, don't touch): `tenant-quota`/`ci*.kql`,
`e2e-cost`/`kubeAudit`, `alertEvents`/`aksEvents`/`maestroMqttConnections`,
Grafana dashboards.

Preserve & verify (do **not** delete): `filter.namespace_router`, the mgmt
`set_cluster_id` Lua, and `filter-shoebox.conf`/`output-shoebox.conf` in the
forwarder — the Geneva/Shoebox fork depends on them. Add a Helm-fixture assertion
on a mgmt+mdsd render (forward → `127.0.0.1:5002` present).

## 11. Open questions

1. `systemd`/`azure-vnet` via `rawLogs` (true single output) or direct outputs
   (3 total, lower fan-out)? — default: via `rawLogs`.
2. Catch-all = negative-filter (exactly-one-table, recommended) vs. full firehose
   (simpler KQL, duplication + dedup needed)?
3. HCP `kubernetesEvents` — is the HCP copy fed/used? If yes, add
   `hcpKubernetesEvents` (14d) mirroring `hcpContainerLogs`.
4. Fold `MonitoringEvents`/Event-Hub/CI tables into `AllAroHcpLogs` later, or keep
   separate? — default: keep separate.
5. Does kustainer fire update policies on ingest? (gates §7.2 — resolve first.)
```
