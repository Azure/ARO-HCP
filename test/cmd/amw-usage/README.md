# AMW Usage

Collect bounded Azure Monitor workspace evidence and generate a standalone HTML
report in one command. Open the resulting HTML directly in your browser.

From the repository's `test` directory:

```bash
go build -o /tmp/amw-usage ./cmd/amw-usage

/tmp/amw-usage collect --azure-cli \
  --workspace "/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.Monitor/accounts/$WORKSPACE_NAME" \
  --start 2026-09-21T04:25:45Z --end 2026-09-21T06:43:40Z \
  --metric kube_pod_info --metric etcd_server_has_leader \
  --run-name integration-window --output /tmp/amw-evidence.json
```

This writes `amw-evidence.json` and adjacent `amw-evidence.html`, and prints both
paths. Raw evidence is never overwritten. Derived HTML is replaced using a
same-directory temporary file, close, and rename. Input/selection/context aliases,
including symlinks and hardlinks, are rejected before collection. Go does not
guarantee atomic rename on non-Unix platforms.

Authenticate using your normal Azure login first. `--azure-cli` uses the Azure
SDK's CLI credential; alternatively set `AZURE_TOKEN_CREDENTIALS=AzureCLICredential`
and omit the flag. Other explicit SDK credential selections, such as
`WorkloadIdentityCredential`, are supported through that environment variable.
The tool only reads Azure public-cloud ARM and Prometheus APIs; it grants no roles
and modifies no resources.

## Selection And Bounds

Repeat `--workspace` and `--metric` as needed. Each exact metric name is queried
only in workspaces where discovery finds it; a name absent everywhere is an error.
Without metric selection, collection performs endpoint/name discovery and six
platform metric queries per workspace, with no per-metric PromQL fanout.
Optional `--selection selections.json` adds a JSON object mapping requested
workspace names or ARM IDs to arrays of exact metric names. Unknown keys or
undiscovered workspace-specific names fail explicitly.

Limits: 1-4 uniquely named workspaces, a 5-minute to 12-hour whole-second window,
5 selected metrics per workspace, 12 workspace/metric pairs, 72 aggregate PromQL
requests, and 104 total requests. Optional `--inventory-metric` adds up to three
workspace/metric pairs with two full-label snapshots each (preceding 12 hours at
run start/end), raising the limits to 78 PromQL and 110 total requests. Collection
is serial with a 300 ms pause, 120-second timeouts, and 8 MiB response bodies
(32 MiB for full-label inventories). No automatic retries, partitioning, or resume.

Every response checkpoints schema-1 evidence, including failures. A partial
collection still generates HTML from readable valid evidence and exits nonzero;
rendering failures do not hide the collection error. `--run-name` and `--prow-url`
are annotations, not verified Prow provenance. Reports describe regional activity
during the explicit window, not necessarily activity attributable to that run.

Platform metrics use one-minute maxima with ten-minute outward-rounded run padding.
An explicit baseline extends the platform start to include the baseline and gap.
Each selected workspace/metric pair makes three PromQL requests without a baseline,
or six with an explicit baseline, aggregated by
`cluster,job,namespace,hostedcontrolplane,prometheus`:

- `range`: stored samples/minute over complete five-minute windows.
- `instant`: distinct stored series over the exact run duration.
- `samples`: total stored samples over the exact run duration, evaluated at run end.
- `newSeries` (explicit baseline only): physical series observed during the run but
  absent from the supplied baseline, evaluated at run end.
- `baselineSeries` (explicit baseline only): distinct stored series in the baseline,
  evaluated at baseline end.
- `baselineSamples` (explicit baseline only): total stored samples in the baseline,
  evaluated at baseline end.

For metric `M`, run duration `D`, baseline duration `B`, and run-end minus
baseline-end offset `O`, all in milliseconds, the instant queries include:

```promql
sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(M[Dms]))
count by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(M[Dms]) unless count_over_time(M[Bms] offset Oms))
count by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(M[Bms]))
sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(M[Bms]))
```

Set subtraction compares full physical labelsets before aggregation. There is no
HA replica deduplication or grouping by replica/pod. The `prometheus` label retains
source attribution where present; missing labels remain unattributed. Prometheus
range selectors are left-open and right-closed: the exact run is `(start, end]`.
New series are not necessarily first-ever series, caused by the run, or complete
quota attribution. Only explicitly selected metrics are measured, not the whole
catalog. A successful empty `newSeries` vector means measured zero; a missing
reference or failed query means unknown, not zero. Raw API errors are retained.
The optional fields extend schema 1; archived evidence without them remains valid.
Query-engine failures do not prove ingestion throttling. Raw bodies, metric labels
and resource IDs can be sensitive.

## Explicit Context

Add `--context ownership.json` to supply run ownership and an explicitly verified
quiet/background comparator. The collector never infers a quiet window, including
the preceding 12 hours. Without a baseline, new-series and baseline references are
omitted, not reported as zero. Archived schema-1 evidence retains its original
query semantics.

The context is preserved in full at top-level `context`, including additional
fields, source URLs, and limitations. Its shape is:

```json
{
  "schemaVersion": 1,
  "run": {
    "start": "2026-09-21T04:25:45Z",
    "end": "2026-09-21T06:43:40Z",
    "prow": "https://prow.example/verified-run"
  },
  "baseline": {
    "start": "2026-09-21T03:30:00Z",
    "end": "2026-09-21T04:00:00Z",
    "reason": "Explain verification and qualifications here",
    "sources": ["https://example.com/baseline-evidence"]
  },
  "clusters": [{
    "id": "observed-cluster-id",
    "name": "customer-cluster-name",
    "resourceId": "/subscriptions/subscription-id/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/customer-cluster-name",
    "namespaces": ["literally-observed-namespace"],
    "sourceURLs": ["https://example.com/ownership-evidence"]
  }],
  "limitations": ["Describe incomplete mappings and comparator qualifications"]
}
```

Run times must match `--start`/`--end` exactly. Baseline is optional; when present,
it must be a whole-second, nonoverlapping prior window of 5 minutes to 4 hours,
start within 32 days of run start, and end no more than 24 hours before run start.
The last bound keeps one-minute platform acquisition bounded (at most about
40 hours including both windows and padding). Baseline reason and source URLs
are required. Cluster IDs/resource IDs and namespaces must be unique, with
nonempty names and source URLs. Provenance URLs must use HTTPS without credentials.
Sources are preserved, not fetched or independently verified by this tool.
Context Prow provenance fills the run annotation; a conflicting `--prow-url` fails.
Only literal supplied mappings establish ownership, never namespace prefixes.

For the example above, the new-series selector is
`M[1800000ms] offset 9820000ms`; baseline totals are evaluated at `04:00:00Z`.
Compare sample rates, not raw totals, when baseline and run durations differ.

## Cold Start And Refresh

For a newly created workspace, start with discovery only (omit `--metric` and
`--inventory-metric`). The explicit run window must still span at least five
minutes. A successful discovery `data: []` is valid: initialization finishes with
no ranking queries, `emptyCatalogs: 1`, and unknown coverage. The database retains
the validated catalog observation and its evidence reference. Missing, failed,
malformed, or null discovery is not an empty observation.

Collect a fresh seed later with **exactly the original start/end, platform window,
and workspace set**, then merge its catalog offline. For personal environments,
set `AZURE_CONFIG_DIR` to the Red Hat CLI configuration directory on every Azure
command (shown here as `$RH_AZURE_CONFIG_DIR`). From the `test` directory:

```bash
AZURE_CONFIG_DIR="$RH_AZURE_CONFIG_DIR" go run ./cmd/amw-usage collect --azure-cli \
  --workspace "$AMW_ID" --start "$START" --end "$END" --output "$FRESH_SEED"
go run ./cmd/amw-usage refresh --db "$DB" --input "$FRESH_SEED" --retry-empty
AZURE_CONFIG_DIR="$RH_AZURE_CONFIG_DIR" go run ./cmd/amw-usage scan --db "$DB" --azure-cli
```

Stop scanners before refreshing. `refresh` makes no cloud requests and requires
no credentials. It validates discovery, rejects changed workspace endpoints or
stored ARM `properties.accountId` incarnations, and transactionally adds only new
metrics with the same three ranking queries used at initialization. Older databases
without stored account IDs can only be checked against their original endpoint;
any newly observed account ID is retained for subsequent checks.

`catalog_refresh` records the new seed hash, original specification fingerprint,
time and counts; `catalog_observation` retains per-workspace empty/nonempty evidence.
Original run metadata, context, normalized platform observations and accepted
nonempty results are unchanged. Fresh platform evidence remains in the new JSON,
not mixed into old totals. Ordinary `scan` resume still uses the original seed
fingerprint; do not pass the fresh seed to `scan --input` on the old database.

`--retry-empty` is optional and only reopens successful, directly measured empty
ranking queries that have no partition-tree links. It clears their accepted
pointer while retaining old attempts. It does not retry failed catchalls or reset
immutable namespace/enrichment plans. Empty means empty **as of that response**,
not a guarantee against late arrival. For latest-data measurement of already
nonempty results, or different windows/context/platform evidence, initialize a
**new database** from the fresh discovery-only seed and scan it; there is no
`--remeasure` shortcut. No pre-creation zeros or quiet baseline are manufactured.
The preceding 12-hour queries are actual stored-series queries, not a verified
quiet comparator; a baseline must be supplied explicitly through context.

## Bounded Namespace Recovery

Roots with no eligible candidates are deferred as `waiting_inventory`, not
measured zero and not an error. After ranking inventory arrives, rerun recovery:
planning is idempotent per root, so a previously planned workspace cannot prevent
another workspace from becoming eligible. Existing plans and their complements
are never expanded or overlapped with later inventory.

Use a **new backup of the final full scan**, not an older enrichment snapshot.
Verify the parent directory and that the destination does not exist first:

```bash
test ! -e namespace-recovery.db && \
  sqlite3 -readonly fullscan.db ".backup 'namespace-recovery.db'"
amw-usage recover-namespaces --db namespace-recovery.db --azure-cli --workers 2
```

The same command resumes the immutable stored plan after cancellation or a crash.
It sends only read-only Prometheus GETs for linked plan children, with at most
1500 durable attempt claims (including retries/crash claims), two global workers
by default and the existing adaptive per-workspace limit of two. Hard limits are
terminal `namespace_inventory_limit`, never recursive partition inputs. Unchanged
terminal remainders are not retried. A spent budget is explicitly unknown.

Candidates are the union of nonempty normalized `(cluster,namespace)` pairs from
accepted ranking `kube_namespace_created`, `kube_pod_info`, and `up` observations
in the original before/end 12-hour windows only. Each root retains the complete
candidate provenance (query, accepted attempt, label/value IDs, window and times)
and its hash. Later inventory changes cannot mutate a resumed plan.

One transaction replaces each unfinished root's entire active tree with direct
exact pair children, one namespace complement per candidate cluster, and one
unknown-cluster complement. Complements cover unseen and empty/absent labels;
inventory alone never establishes completeness. Count/sum grouping and original
evaluation times/lookbacks are unchanged; no time splitting or growth-set claims.
Old edges become inactive; queries, attempts and observations remain historical.
Identical accepted queries are reused, with displaced legacy links retained in
`namespace_inventory_edge_history`. Children remain non-ranking, roots ranking.

Parents wait for every planned child to terminate. Only fully accepted, valid
children produce an additive parent result; failed roots become
`namespace_inventory_incomplete`. Their active successful leaves remain useful
disjoint lower bounds, without also counting superseded successful partitions.
Adjacent HTML is rendered automatically using the current offline renderer.
Enrichment from other snapshots is **not** merged by this command.

## Durable Sample Enrichment

Use a **new online SQLite backup**, not the active scanner database or a filesystem
copy of a WAL database. Leave the original scanner running. Verify the destination
does not exist before running `.backup` (SQLite can otherwise overwrite it).

```bash
sqlite3 -readonly fullscan.db ".backup 'events-analysis.db'"
amw-usage enrich --db events-analysis.db --plan plan.json --azure-cli
# Resume after cancellation or a crash; the plan is already in SQLite:
amw-usage enrich --db events-analysis.db --azure-cli
```

The public plan format is:

```json
{
  "windows": [
    {"name":"baseline","start":"2026-09-21T03:40:00Z","end":"2026-09-21T03:45:00Z"},
    {"name":"run-minute","start":"2026-09-21T05:00:00Z","end":"2026-09-21T05:01:00Z"}
  ],
  "metrics": [
    {"workspace":"hcps-westus3","name":"kube_pod_info","labels":true},
    {"workspace":"services-westus3","name":"kube_job_status_succeeded","labels":true,
     "matchLabels":{"namespace":"velero","cluster":"int-westus3-mgmt-1"}}
  ]
}
```

Limits are 1-3 distinct windows of 1-30 minutes, 1-4 catalog workspace/metric
pairs, and at most 24 queries. Every metric is queried in every window unless its
optional `windows` list selects specific window names (e.g. baseline and that
workspace's peak). Times must
be whole-second RFC3339 within the original run or its explicitly supplied baseline;
padding and gaps are not valid. Baseline context is validated, not independently
verified. No endpoints, arbitrary expressions, regex matchers or partition fanout
are accepted. Optional `matchLabels` supports exact `cluster`, `namespace`, and
`job` values and applies identically to both roles. Names may be workspace names or
the original ARM IDs, matched case-insensitively. `labels:false` requests only grouped counts.

For selector `M` and window duration `W` in milliseconds, evaluated at its end:

```promql
sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(M[Wms]))
count_over_time(M[Wms])
```

The immutable normalized plan is in `enrichment_plan.spec_json` with a SHA-256
fingerprint. Windows use `window.kind=samples:<startUnix>:<endUnix>`. Logical roles
live in `enrichment_query(plan_id,window_id,workspace_id,metric_id,role,query_id,selector,grouping)`;
the primary key is `(plan_id,window_id,workspace_id,metric_id,role)` and `plan_id=1`.
Roles are `samples_window` and `inventory_samples`. New execution queries retain
those kinds with `ranking=0`, but identical existing requests are reused **without
changing their kind, ranking, window, or accepted attempt**. In particular, a whole
run or whole baseline may reuse an existing ranking or cached query. Renderers must
join this link table for enrichment roles/windows, not filter `query.kind`.
Resuming an older enrichment snapshot transactionally backfills these links.
A conflicting plan or role link fails atomically;
use a fresh snapshot for a different experiment. Exact params, attempt HTTP status,
byte counts, error prefixes, retries and accepted observations use the existing
schema-1 ledger. Full-label observations store **sample-count weights**, not just
one row per series. Compare their sum to grouped counts for the identical scope;
unequal durations require rates rather than subtracting totals.

`enrich` filters claims, expired-lease recovery and concurrency to request IDs linked
to the selected enrichment roles; its summary counts logical roles. An already
accepted identical request requires zero HTTP calls. A pending identical ranking
request explicitly required by the plan executes once and legitimately contributes
to both coverages. Unlinked ranking queries and leases remain untouched, are never
resumed, and do not count against enrichment concurrency. No partition planning
runs. Rate limits and retry budgets remain workspace-coordinated; a hard limit or
invalid response is explicitly blocked while other work continues. Responses are
streamed in 128-observation batches with 8 MiB grouped and 32 MiB full-label caps.
Both roles require positive integer counts; incomplete responses never publish.
Failure diagnostics retain a bounded, token-redacted validation reason and offending
raw value before the response prefix, including failures beyond the 64 KiB prefix.
Cancellation releases owned claims; crash recovery fences expired attempts.

The command prints an enrichment-only coverage summary and generates adjacent HTML
after normal completion, including partial coverage (nonzero exit). Ranking status
may remain incomplete, independently. Offline rendering needs no credentials.

## Offline Rendering

Regenerate HTML from existing evidence without credentials or Azure calls:

```bash
/tmp/amw-usage render \
  --input /path/to/amw-evidence.json \
  --output /tmp/amw-report.html
```

Repeat this command to replace derived HTML, then reopen or refresh it in your
browser. The evidence input is protected against replacement.

## Tests And Windows

```bash
go test ./util/amwusage ./cmd/amw-usage
GOOS=windows GOARCH=amd64 go build -o /tmp/amw-usage.exe ./cmd/amw-usage
```

On Windows, build natively with `go build -o amw-usage.exe ./cmd/amw-usage` and
use the same commands with Windows paths. Open the generated HTML directly.

The default Go tests use synthetic fixtures and temporary directories. Cached
evidence regressions are opt-in via `AMW_USAGE_ARCHIVE`, `AMW_USAGE_CONTEXT`,
`AMW_USAGE_INVENTORY`, `AMW_SCAN_TEST_SEED`, `AMW_EVENTS_DATABASE`,
`AMW_SAMPLE_DATABASE`, and `AMW_BUDGET_RECOVERY`; keep those inputs outside the
repository. `AMW_SCAN_BENCHMARK_DB` optionally selects a benchmark output path.

`AMW_DATABASE_BROWSER=1` enables the compact report's browser checks in Go tests.
These require Node 22+ and Chrome (`CHROME` overrides the executable) and exercise
desktop/mobile, disabled JavaScript, and blocked/available ECharts CDN behavior.
They are optional and can access the pinned ECharts CDN. The other browser scripts
cover JSON and legacy report layouts and need a matching report supplied as an
argument; they are not all applicable to the compact SQLite renderer.
Review these executable `.mjs` scripts manually before merge, including Chrome's
`--no-sandbox` flag, and run them only against trusted local reports. Never commit
cached databases, raw evidence, credentials, or private screenshots.

## Durable Full-Catalog Scans

Initialize from a schema-1 archive containing the run, workspace endpoints and
complete discovered metric catalogs:

```sh
go run ./cmd/amw-usage scan --db usage.db --input amw-evidence.json --azure-cli --workers 4
go run ./cmd/amw-usage status --db usage.db
go run ./cmd/amw-usage scan --db usage.db --azure-cli
```

The last command resumes without the seed or window arguments. Supplying a seed
again requires the identical file fingerprint. SIGINT and SIGTERM release this
process's claims; after a crash, live claims expire after 60 seconds. The scanner
does not follow redirects or modify Azure resources. It makes read-only PromQL
requests, which do consume query capacity.

Each catalog pair gets three grouped queries: rolling 12-hour series counts at
run start and end, and observed sample counts over the run at run end. Grouping
is `cluster,job,namespace,hostedcontrolplane,prometheus`. These are retained sampled
series, not received-series quota accounting. Cached queries are reused only
when the complete persisted path and query parameters match. Other cached
results remain separate normalized evidence, not substitutes for ranking queries.

`state: complete` means scheduling has finished, including terminal blocked
queries. Only `coverageComplete: true` means every ranking query succeeded.
The status includes blocked classifications and the earliest persisted wake time.
Hard series/cost limits trigger bounded, durable label partitioning rather than
unchanged retries. Exact cluster selectors, namespace suffixes, metric-specific
jobs and instance selectors preserve exhaustive coverage; parents publish only
after all active children succeed. Depth and descendant budgets can leave a
measurement blocked. Reports retain successful-leaf lower bounds separately from
complete totals. Authorization failures block the affected workspace. There is no
automatic unblock command: fix access and review/reset affected query state and
`workspace.blocked_reason` deliberately rather than repeatedly retrying failures.

## SQLite Reader Contract

Schema version is `PRAGMA user_version = 1`. Public Go API:

```go
store, err := amwusage.OpenScanStore(path)
err = store.Initialize(ctx, seedReader) // once; idempotent for identical input
err = store.Scan(ctx, amwusage.ScanOptions{Workers: 4, Credential: credential})
summary, err := store.Summary(ctx) // amwusage.DBSummary
err = store.Close()
```

Renderers can open `file:...?...mode=ro` with the `sqlite` database/sql driver.
Use a read transaction for a consistent live snapshot. Never read `observation`
alone: it also contains unpublished staging rows. Read `accepted_observation`,
which joins only the accepted attempt of succeeded queries.

- `run`: singleton identity, seed fingerprint, windows, job/build/prow, original
  `context_json`, planning state and execution state. Context is supplied evidence,
  not a claim that sources were fetched or independently verified.
- `window`: `before12h`, `end12h`, `run`, optional explicit `baseline`.
- `workspace`: ARM ID, name, endpoint, persisted cooldown/concurrency/access block.
- `metric`: workspace, lowercase canonical name, first display name, catalog flag.
- `query`: workspace/metric/window IDs, `kind`, `ranking`, exact path/encoded
  params/expression, evaluation time/lookback/grouping, state/retry/lease and
  accepted/current attempt IDs. Ranking kinds are `before12h`, `end12h`, `run`.
  Archive-only kinds are `instant`, `range`, `samples`, `new_series`,
  `baseline_series`, `baseline_samples`, `inventory_before`, `inventory_end`.
- `attempt`: status, bytes read, duration, numeric API cost, safe request IDs,
  classification, warnings/stats metadata, bounded gzip error prefix. No raw
  successful body is stored. `owner='seed'` identifies imported attempts.
- `accepted_observation`: query/workspace/metric/kind/ranking plus attempt ID,
  labelset ID, timestamp, numeric `value`, exact `integer_value` where applicable,
  and nullable `invalid_value`. A succeeded query may still contain invalid sample
  values; renderers must display that gap rather than treating it as zero.
- `labelset`: SHA-256 identity, collision-checked against normalized membership.
  New rows leave the legacy `canonical` column empty instead of repeating label
  strings. Existing canonical JSON is neither required nor rewritten. Labels and
  values use AMW lowercase identity. `labelset_member` references unique
  `label_name` and `label_value` entries; membership is stored once per labelset.
- `evidence`: archived request provenance without successful response bodies.
  `platform_observation` references evidence ID, metric, labelset, timestamp and
  numeric/invalid value. Successful resource/catalog payloads are represented by
  workspace/catalog tables, not retained blobs.

All database timestamps are Unix seconds (REAL), except `duration_ms`. Ranking
success is published only after complete response parsing, EOF and envelope
validation. Each staging batch and publication is fenced by owner, attempt and
unexpired lease. SQLite verifies WAL mode on open and applies foreign keys, FULL
synchronous durability and a 10-second busy timeout to every writer connection,
including replacements. Each store has one connection, so its caller goroutines
queue locally. Independent stores/processes use short `BEGIN IMMEDIATE`
transactions: SQLite permits only one writer per database at a time, while WAL
read snapshots do not block writers. Contending writers wait up to the busy
timeout; WAL does not allow simultaneous writes. Automatic checkpointing retains
SQLite's default 1000-page threshold. Keep the database together with its WAL/SHM
files while a process is using it; use SQLite backup for live copies.

The existing JSON collect/render workflows remain supported. `render --input`
detects SQLite from its 16-byte header, irrespective of filename extension,
without loading the database into memory. `scan` generates an adjacent same-stem
HTML report on completion using an independent read-only snapshot. Cancellation
skips automatic rendering for prompt shutdown; use `render` afterward. Report paths cannot overwrite
the database, seed, or SQLite sidecars, including aliases.

No schema migration runs for the normalized-label change. Do not mix the old
writer (which expects canonical JSON) with the new writer on the same database.
Wait until the old scanner stops before switching binaries. Old canonical strings
may be compacted after scheduling completes with `compact --db usage.db`. This
also removes unreferenced dictionaries, checkpoints the WAL, and vacuums the
database. Compaction refuses an active scan; it preserves accepted observations
and historical request evidence.
