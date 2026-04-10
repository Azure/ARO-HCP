# ci-triage

CI failure triage tool for ARO-HCP Prow e2e tests. Ingests job data from GCS into a local SQLite database, then provides failure analysis, onset detection, message deduplication, and PR baseline comparison.

## Quick Start

```bash
make build

# One-shot: ingest + analyze (auto-syncs fresh data from GCS)
./ci-triage summary --since 7d
./ci-triage failures int --since 14d
./ci-triage pr 4630

# Build log (fetched live, not from DB)
./ci-triage build-log <GCSWEB_URL> int --lines 200
```

## Commands

| Command | Description |
|---|---|
| `summary` | Cross-env health scan with pass rates and fleet-wide failure correlation |
| `failures ENV` | Deep evidence packet: failure groups, onset detection, per-job breakdown |
| `pr NUMBER` | PR triage with periodic baseline comparison (`[baseline]` vs `[NEW]`) |
| `build-log URL [ENV]` | Raw build log tail with timestamp visibility |
| `ingest` | Explicit data ingestion (query commands auto-sync by default) |
| `serve` | HTTP server with continuous ingestion polling |

## How It Works

1. **Ingest** fetches job metadata (`finished.json`) and test results (`junit.xml`) from GCS, parses them, and stores structured data in SQLite.
2. **Query** commands read from the DB using SQL joins and aggregations — no network calls needed.
3. **Auto-sync** (default) runs ingestion before each query to ensure fresh data. Use `--no-sync` to skip.
4. **Retention** auto-prunes data older than 30 days during sync. Override with `ingest --retain 90d`.

## Data Flow

```
GCS (test-platform-results)
  └─ finished.json, junit.xml per job
       │
       ▼
  Ingester (parallel fetch, 20 workers)
       │  parse JUnit XML
       │  normalize failure messages for dedup
       │  extract snowflake timestamps from build IDs
       ▼
  SQLite DB (~/.cache/ci-triage/ci-triage.db)
       │  jobs table (env, state, revision, PR, timestamps)
       │  test_results table (test name, status, message, normalized key)
       ▼
  Analysis (SQL queries: failure groups, onset, fleet correlation)
       │
       ▼
  Output (markdown or JSON)
```

## Database

Default location: `~/.cache/ci-triage/ci-triage.db` (respects `$XDG_CACHE_HOME`).

Override with `--db /path/to/db` on any command.

The DB is safe to delete — it rebuilds incrementally on the next run. A 7-day rebuild takes ~15 seconds; 30 days takes ~60 seconds. Auto-pruning removes data older than 30 days during sync.

Approximate sizes: 7 days ~9 MB, 30 days ~28 MB.

## Service Mode

For continuous operation (e.g., in a pod called by GitHub Actions):

```bash
./ci-triage serve --listen :8080 --poll 5m
```

Endpoints: `GET /api/v1/summary`, `/api/v1/failures/{env}`, `/api/v1/pr/{number}`, `/healthz`

## Development

```bash
make test           # run unit tests (uses :memory: SQLite)
make build          # build binary
make clean          # remove binary
```

The tool is also available as a Claude Code skill: `/triage int` or `/triage pr 4630`.
