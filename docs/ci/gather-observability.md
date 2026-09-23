# E2E Observability Artifacts

The existing `aro-hcp-tests gather-observability` step also runs best-effort AMW
metric-usage analysis through `test/util/amwusage`. It uses the rendered service
and HCP workspace IDs and the same Azure credential as alert collection. No
additional invocation or provisioning is needed.

Artifacts in `--output` include:

- `observability-summary.html`: existing alerts, panels and utilization, plus an
  **AMW Metric Usage** tab with the embedded report.
- `amw-usage-summary.html`: standalone report, named for the Spyglass HTML lens.
- `amw-usage-seed.json`: checkpointed discovery and platform evidence.
- `amw-usage.db`: resumable query/attempt ledger and accepted observations.

Both HTML reports start with an explicit in-progress/unknown status before cloud
collection. Normal completion replaces them with available results. Incomplete
AMW coverage, throttling, authentication and rendering errors are logged and
shown as diagnostics, not new e2e gates. Existing alert JUnit failures and other
gather errors still fail the command. Missing measurements are not zeros.

A workspace with explicitly failed discovery or endpoint lookup remains in the
database and report as blocked/unknown, with no queries scheduled for it. Healthy
workspaces still scan. The original seed is unchanged. Recovery of a failed
endpoint requires a fresh seed/database, not an invented endpoint or empty catalog.

## Timeout Contract

`GATHER_OBSERVABILITY_TIMEOUT` is a Go duration for the **whole gather command**,
not just AMW. It defaults to `4m`, leaving headroom under the existing external
five-minute step timeout. Values must exceed `1m`. The final minute is reserved
for scanner cleanup and offline reporting; all cloud acquisition shares the
earlier deadline. AMW starts concurrently with standard gathering, with two scan
workers (initially one request per workspace). Standard queries use at most two
workers, so aggregate query concurrency is at most four. AMW retains its existing
per-workspace throttling/backoff behavior.

The companion `openshift/release` change must pair an external gather-step
timeout of **75 minutes** with **`GATHER_OBSERVABILITY_TIMEOUT=65m`**, and preserve
artifact collection after errors. Setting only the environment variable while
retaining the five-minute external timeout will kill collection prematurely.
The larger budget does not guarantee full catalog coverage.

Cancellation is cooperative, not an OS-level execution guarantee. The CI renderer
checks context during model construction and SQL reads, and caps output at 16 MiB.
It refuses oversized models before rendering (10,000 metrics, 100,000 queries,
500,000 observations, 100,000 platform observations, 1,000,000 label memberships,
256 label names, 16 MiB of label text, 32 MiB of expanded labels, and 1 MiB of metric
names). Enrichment rendering has a stricter
10,000-observation limit. Exceeding a limit leaves a diagnostic and intact database,
never truncated totals. Offline `amw-usage render` is not subject to these CI caps.
When the reporting context expires, gathering joins its AMW worker and preserves
the last combined-page checkpoint instead of starting another full render. The
external timeout remains the final bound for filesystem stalls and bounded but
non-interruptible JSON/sort operations.

AMW freezes the existing report window to whole seconds and caps its end at
collection start, excluding the unelapsed future portion of the e2e window.
Windows must be between five minutes and twelve hours; invalid windows are
reported, never silently shortened or padded. The library retains its platform
context around the window and its preceding twelve-hour series comparisons;
these are not assertions of a quiet baseline or proof of test attribution.

After downloading artifacts, the standalone tool can render the database offline:

```bash
amw-usage render --input amw-usage.db --output amw-usage-summary.html
```

After an external hard kill, preserve any `amw-usage.db-wal` and
`amw-usage.db-shm` alongside the database. Normal gathering waits for scan workers
and closes the writer before rendering its consistent read-only snapshot.
