# depctl

`depctl` prepares, validates and investigates ARO-HCP dependency and tool
updates locally. It runs the repository's own generators and consumers against an
isolated candidate, so a broken bump is caught before a pull request exists.
It does not publish: no command in this build pushes, opens a pull request,
comments or changes scheduling.

This build implements the **tools** profile. Publication, hold recovery, retest
and scheduling require an authorized execution integration; the engine refuses
to publish without an injected writer and the CLI never constructs one.

## The problem

Dependency updates were decided in workflow prose and validated after
publication. A bump that broke a repository consumer became a pull request
first, and a held proposal with failing current-head CI was diagnosed from
partial evidence, so the same failure returned on the next attempt. `depctl`
moves the deterministic parts of that loop into code that can be run locally:
discovery, planning, candidate preparation, consumer validation, proposal
packaging and read-only failure investigation.

## The workflow

| Step | Command | What it produces |
| --- | --- | --- |
| Discover pins | `inventory` | one row per upstream family with exact current pins and, when online, the latest release |
| Choose targets | `plan` | exact target versions, base revision, permitted output classes and required consumers |
| Prepare a candidate | `apply` | an isolated worktree, native generation, a committed candidate revision |
| Validate | `verify` | baseline and candidate consumer results with one verdict |
| Package | `proposal` | title, branch, marker, patch and expected remote state, with no authorization |
| All of the above | `run --mode prepare` | the same phases in one invocation, stopping before publication |
| Observe and diagnose | `maintenance inspect`, `evidence prow` | hold and CI evidence, retrieved run artifacts, one diagnosis |
| Exercise without publication | `run --mode simulate` | a full lifecycle against fixture repositories and fake services |

Publication, CI observation, maintenance actions and continuation exist in the
engine but are reachable only with injected adapters; that separation is
described in [docs/design.md](docs/design.md).

## Commands

Commands that produce a record print it on stdout, or write it to `--output`;
diagnostics go to stderr. A record is `schemaVersion: depctl/v1`. `version`
prints provenance text, and `run --mode simulate` prints its scenario report.

### inventory

```bash
depctl inventory --repo PATH [--profile PROFILE] [--offline] [--revision SHA] [--output FILE]
```

Reads the pinned versions in the working tree: bingo tool modules and
`dev-infrastructure/openshift-ci/versions.mk`. With `--offline=false` it also resolves
the latest stable upstream release, which needs `gh` for GitHub releases and `go`
for modules.

```bash
depctl inventory --repo . --offline --output inventory.json
```

Refuses a `--revision` that is not the checked-out revision, because pins are
read from the working tree, not from an arbitrary Git object. A failed lookup is
recorded as an incomplete inventory; it is never reported as current.

### plan

```bash
depctl plan --input inventory.json [--profile PROFILE] --target FAMILY=VERSION... [--output FILE]
```

Turns an inventory plus explicit targets into a plan: exact versions, the base
revision, the permitted edit kinds and the fixed consumer IDs. Planning performs
no install, generation or write.

```bash
depctl plan --input inventory.json --target github.com/google/yamlfmt=0.21.1 --output plan.json
```

A family already at the requested version is reported as `no_change`. Only one
family is prepared per run; further eligible families appear under `skipped`
with the reason. A downgrade, an unknown family or an unsupported profile is
rejected.

### apply

```bash
depctl apply --repo PATH --plan plan.json --candidate PATH [--log-dir DIR] [--dry-run] [--output FILE]
```

Creates an isolated worktree from the plan's base revision, applies the planned
edits with native tooling (`bingo get` for a tool module, a `versions.mk`
assignment and the matching Dockerfile argument for a CI tool), checks that
generation converges, commits the result and records digests plus the candidate
revision.

```bash
depctl apply --repo . --plan plan.json --candidate /tmp/candidate --output candidate.json
```

Use `--dry-run` to declare the operations without creating a candidate. The
source checkout is never written: an edit path that resolves inside it, a base
revision that moved, or uncommitted edits to a planned file are refused. The
worktree is kept on success so `verify` and `proposal` can use it, and removed
when the record cannot be written.

### verify

```bash
depctl verify --repo PATH --plan plan.json --candidate candidate.json \
  [--log-dir DIR] [--baseline-tool NAME=PATH...] [--candidate-tool NAME=PATH...] [--output FILE]
```

Runs every required consumer on a fresh baseline worktree and on the candidate,
then reports `validated`, `candidate_regression`, `baseline_blocked` or
`incomplete`. Consumers come from compiled policy, not from the plan record, and
the candidate bytes are re-digested before and after the run.

```bash
depctl verify --repo . --plan plan.json --candidate candidate.json --output validation.json
```

The candidate must belong to the plan and base and carry the planned values; a
record that only matches its own hashes is refused. A missing prerequisite or a
mismatched executable version is `not_run`, which blocks validation; it is never
a pass. Pass `--baseline-tool NAME=PATH` and
`--candidate-tool NAME=PATH` when a gate needs two different compiler versions.

### proposal

```bash
depctl proposal --repo PATH --plan plan.json --candidate candidate.json \
  --validation validation.json [--artifact-dir DIR] [--output FILE]
```

Packages a validated candidate into a proposal record: title, branch, family
marker, patch digest and the expected remote state. It recomputes the verdict
from the recorded results and refuses an incomplete, contradictory or duplicate
result set. A proposal confers no authorization.

```bash
depctl proposal --repo . --plan plan.json --candidate candidate.json \
  --validation validation.json --output proposal.json
```

### run --mode prepare

```bash
depctl run --repo PATH [--profile PROFILE] --mode prepare [--offline] \
  [--target FAMILY=VERSION...] [--baseline-tool NAME=PATH...] [--candidate-tool NAME=PATH...] \
  [--dry-run] [--keep-candidate] [--work-dir DIR] [--state-dir DIR] [--output FILE]
```

Executes discover, plan, apply, verify and proposal in one invocation and stops
at `awaiting_authorization`. Use it for the normal local preflight.

```bash
depctl run --repo . --mode prepare --target github.com/mikefarah/yq/v4=4.54.2 \
  --work-dir /tmp/depctl-run --output run.json
```

`--dry-run` stops after planning and declares the operations. `--keep-candidate`
leaves the worktree for inspection. An omitted `--work-dir` falls back to a
temporary directory outside the repository.

### run --mode simulate

```bash
depctl run --mode simulate [--scenario NAME] --work-dir DIR [--output FILE]
```

Drives the complete lifecycle against a disposable repository and fake release,
forge and evidence services. It accepts `--scenario` and `--work-dir` only.

```bash
depctl run --mode simulate --scenario held-failure --work-dir /tmp/depctl-sim
```

### maintenance inspect

```bash
depctl maintenance inspect --repository OWNER/REPO --pr N [--repo PATH] [--base SHA] \
  [--investigate] [--comparison-url URL...] [--state-dir DIR] [--evidence-dir DIR] [--output FILE]
```

Read-only assessment of an owned proposal: identity, hold provenance,
current-head CI, changed files, retrieved run artifacts and a diagnosis. It
never releases a hold, retests or merges.

```bash
depctl maintenance inspect --repository Azure/ARO-HCP --pr 7892 \
  --base <merge-base> --investigate --evidence-dir /tmp/evidence --output decision.json
```

A human or unexplained hold, a consumed per-head recovery budget, a failing or
pending current head, or missing evidence keeps the decision blocked.

### evidence prow

```bash
depctl evidence prow --url PROW_URL --output-dir EMPTY_DIR [--output FILE]
```

Retrieves `finished.json` and `build-log.txt` for one canonical Prow run URL and
publishes a manifest last. It preserves the bucket and job path from the URL, so
it never guesses an alternative bucket.

```bash
depctl evidence prow --url https://prow.ci.openshift.org/view/gs/<bucket>/<job>/<run> \
  --output-dir /tmp/run-evidence --output evidence.json
```

Retrieval failure is incomplete evidence, not a diagnosis. A genuine `FAILURE`
result is valid fetched evidence.

### version

```bash
depctl version [--output-json]
```

Prints the commit, build date, platform and record schema version; `--output-json`
prints the same as JSON. `--verbosity` (global) sets the log level.

## Side effects

| Command | Reads | Writes |
| --- | --- | --- |
| `inventory` | tracked pins; `bash` and `go` for local lookups | nothing, or `--output` |
| `plan` | the inventory record | nothing, or `--output` |
| `apply --dry-run` | the plan record | nothing, or `--output` |
| `apply` | the source checkout | a committed candidate worktree, kept on success |
| `verify` | baseline and candidate worktrees | consumer logs, a temporary baseline worktree |
| `proposal` | the candidate worktree | a patch file and `--output` |
| `run --mode prepare` | the source checkout, optionally upstream releases | candidate, logs, records, journal |
| `run --mode simulate` | a fixture repository | a scratch directory under `--work-dir` |
| `maintenance inspect` | `gh api` and public Prow artifacts | evidence files, journal, `--output` |
| `evidence prow` | the public Prow bucket | artifacts and a manifest in `--output-dir` |
| `version` | build metadata | nothing |

No command pushes, opens a pull request, comments, dispatches a workflow or
changes scheduling. Consumer logs, candidate worktrees and patches are allocated
outside the repository unless you name a directory yourself.

## Prerequisites

Offline discovery needs `bash` for the CI tool mapping and `go` for the bingo
module metadata. Candidate preparation and the tools consumers need the tools the
repository already uses: `bingo`, `make`, `go`, `jq`, and `bicep` for a Bicep
candidate. `maintenance inspect` needs an authenticated `gh`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | the requested operation completed; inspect `outcome`, `phase` and `complete` |
| 2 | invalid input, schema, flag or unsupported request |
| 3 | incomplete evidence, missing prerequisite or blocked operation |
| 4 | candidate validation failed |
| 5 | stale base, source, head or candidate; re-plan or re-apply |
| 6 | adapter or internal failure; the record says whether a side effect is uncertain |

Exit 0 does not mean "verified" or "published". A run that ends
`awaiting_authorization` still needs an authorized publisher.

## Troubleshooting

- **Exit 5 (`stale`)**: the base revision moved, a planned file is dirty, or the
  candidate worktree no longer matches its record. Re-run `plan` and `apply`.
- **Exit 3 (`blocked` / `incomplete`)**: a consumer did not run, or evidence is
  missing. The record lists `notRun` or `missing` entries; install the pinned
  tool or pass the phase-specific executable and retry.
- **Consumer `not_run` behind a green build**: the `PATH` executable reports a
  different version than the plan expects. Select it explicitly rather than
  trusting `PATH`.
- **`simulate` rejects `--repo` or `--target`**: simulation never touches a real
  checkout; use `--mode prepare` for a real repository.
- **No record printed**: the command failed before producing one; read stderr and
  the exit code rather than parsing stdout.

## Simulation scenarios

| Scenario | Proves |
| --- | --- |
| `lifecycle` | discover through proposal, simulated publication, CI observation, maintenance decision and continuation |
| `no-update` | a pin already at the target ends as `no_change` with no candidate and no writes |
| `generation-fails` | a failing native generator ends the run before a proposal |
| `validation-regression` | a baseline-passing, candidate-failing consumer blocks publication |
| `held-failure` | a retry-exhaustion hold with a failing current head is investigated, compared with the same job on another revision, and diagnosed |
| `ci-pin-pair` | a CI tool bump edits `versions.mk` and the Dockerfile argument together and the consistency consumer passes |

## Documentation

- [docs/design.md](docs/design.md): lifecycle model, records and identity, trust
  boundaries, policy versus adapters, failure investigation.
- [docs/contract.md](docs/contract.md): preserved policy constants with their
  sources, open decisions and what each blocks.
