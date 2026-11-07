# depctl

`depctl` runs the ARO-HCP dependency automation lifecycle locally.

It exists to shorten the fix-after-fix loop: deterministic policy, candidate
preparation, validation and evidence collection run through the same code path
the CLI uses, so a change can be proved locally against the repository's real
generators and consumers before anything is published.

This build implements the **tools** profile only. Publication, hold recovery
and scheduling require an authorized execution integration and are not reachable
from the binary: `Publish` exists in the engine but refuses to run without an
injected writer, and no command constructs one.

## Status

| Capability | State |
| --- | --- |
| Offline pin inventory (bingo modules, openshift-ci tool versions) | implemented |
| Online upstream resolution (`gh`, `go list -m`) | implemented, opt-in |
| Plan with explicit targets and fixed consumer IDs | implemented |
| Isolated candidate worktree, native `bingo get` generation | implemented |
| Baseline/candidate consumer validation | implemented |
| Proposal packaging (title, branch, marker, patch digest) | implemented |
| Dry run that declares operations without side effects | implemented |
| Read-only owned-PR assessment: ownership, holds, current-head CI | implemented |
| Failure investigation: Prow evidence, changed files, comparison, diagnosis | implemented |
| Trusted publication | not registered (needs D03–D07 authority) |
| Hold release, retest, compiler refresh | not registered (write authority) |
| Go/npm/Actions/image profiles, security and native-Dependabot parity | not started |

## Build and test

```bash
make -C tooling/depctl build
make -C tooling/depctl test
make -C tooling/depctl test-scenarios
```

The module is a member of the repository Go workspace, so `make lint`,
`make test-unit` and `make verify-vendor` cover it like any other module.

## Commands

Every command writes versioned JSON (`schemaVersion: depctl/v1`) to `--output`
and prints diagnostics to stderr. `--output` is optional; when omitted only the
human summary is printed.

```text
depctl inventory --repo PATH [--offline] [--revision SHA] [--output FILE]
depctl plan --input inventory.json --target FAMILY=VERSION... [--output FILE]
depctl apply --repo PATH --plan plan.json --candidate PATH [--dry-run] [--output FILE]
depctl verify --repo PATH --plan plan.json --candidate candidate.json [--output FILE]
depctl proposal --repo PATH --plan plan.json --candidate candidate.json \
    --validation validation.json [--output FILE]
depctl run --repo PATH --mode prepare [--target FAMILY=VERSION...] \
    [--dry-run] [--work-dir DIR] [--state-dir DIR] [--output FILE]
depctl run --mode simulate --scenario NAME --work-dir DIR [--output FILE]
depctl maintenance inspect --repository OWNER/REPO --pr N [--base SHA] \
    [--investigate] [--comparison-url URL...] [--state-dir DIR] [--output FILE]
depctl evidence prow --url PROW_URL --output-dir EMPTY_DIR [--output FILE]
depctl version [--output-json]
```

### Side effects

| Command | Reads | Writes |
| --- | --- | --- |
| `inventory` | tracked pins, process environment for `bash`/`go` lookups | nothing except `--output` |
| `plan` | the inventory record | nothing except `--output` |
| `apply --dry-run` | the plan record | nothing except `--output` |
| `apply` | source checkout | creates the candidate worktree only |
| `verify` | baseline and candidate trees | consumer logs, temp baseline worktree |
| `proposal` | candidate worktree | patch file, `--output` |
| `run --mode prepare` | source checkout, optional upstream lookups | candidate, logs, records, journal |
| `run --mode simulate` | fixture repository | a scratch directory under `--work-dir` |
| `maintenance inspect` | `gh api` reads, public Prow artifacts | evidence files, journal, `--output` |
| `evidence prow` | public Prow bucket | artifacts and manifest in `--output-dir` |

No command pushes, opens a pull request, comments, dispatches a workflow or
changes scheduling.

`apply`, `verify`, `proposal` and `run --mode prepare` never modify the source
checkout: they fail with `stale` when the checkout's base revision moved or when
it has uncommitted edits to a file the plan would touch.

### Offline prerequisites

Offline discovery needs `bash` (for `dev-infrastructure/openshift-ci/update-tool-versions.sh --metadata-json`)
and `go` (for `go mod edit -json` over `.bingo/*.mod`). Candidate generation and
the tools-profile consumers need the tools the repository already uses:
`bingo`, `make`, `go`, `jq`, and `bicep` for a Bicep candidate.

An unmet prerequisite is reported as `not_run` and blocks validation with
`incomplete`. It is never treated as a pass.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | the requested operation completed; inspect `outcome`, `phase` and `complete` (a completed no-change or awaiting-authorization run is 0) |
| 2 | invalid input, schema or unsupported request |
| 3 | incomplete evidence, missing prerequisite or blocked operation |
| 4 | candidate validation failed |
| 5 | stale base, source or head; re-plan |
| 6 | adapter or internal failure; the record says whether a side effect is uncertain |

Exit 0 does not mean "verified" or "published". A run whose `outcome` is
`awaiting_authorization` needs an authorized publisher.

## Simulation

`run --mode simulate` builds a disposable repository with the same pin layout
(`.bingo/*.mod`, `dev-infrastructure/openshift-ci/versions.mk`, a bingo
generator and the CI version consumer) and drives the production engine against
fake release, forge and evidence services.

| Scenario | Proves |
| --- | --- |
| `lifecycle` | discover → plan → candidate → validate → proposal → simulated publish → CI observation → maintenance decision → continuation |
| `no-update` | a pin already at the target ends as `no_change` with no candidate and no writes |
| `generation-fails` | a failing native generator ends the run before a proposal |
| `validation-regression` | a baseline-passing, candidate-failing consumer blocks publication |
| `held-failure` | a retry-exhaustion hold with a failing current head is investigated, compared against a trusted run and diagnosed |

The simulation asserts its own capability split: the candidate stage is built
without a forge writer, and the publication stage never executes the candidate
tree.

## Trust boundaries in this build

- Policy code (`internal/policy`) is pure: no credentials, no processes, no
  network, no environment.
- Consumer commands come from a fixed registry. Plans may only name an ID and
  typed parameters; no record can supply a command.
- Publication re-reads base revision, ownership and the single-flight slot, and
  reconciles a lost response instead of retrying blindly.
- When a journal is configured, publication writes its intent before the effect
  and its observation afterwards. The journal lock is single-host only and is
  documented as insufficient for multiple publishers.
- Failure investigation classifies only three ways: `candidate_regression`,
  `supported_unrelated_failure` and `unknown_incomplete`. Retrieval failure is
  never a diagnosis, and confidence is never evidence.

## Documentation

- `docs/contract.md` — preserved policy constants with their sources, the open
  decisions and what each blocks, the package map, the lifecycle states and the
  trust boundaries.
