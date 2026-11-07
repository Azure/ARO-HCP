# depctl contract

Durable constraints, preserved policy and decisions for the dependency automation
tool. Session evidence (baseline deltas, remote pull-request snapshots and
executed-command reports) is not kept in the repository.

## Supported scope

The tools profile is implemented: offline and online pin inventory, planning with
explicit targets, isolated candidate preparation with native generation,
baseline/candidate consumer validation, proposal packaging, and read-only
maintenance investigation.

Publication, hold release, retest, compiler refresh and scheduling require an
authorized execution integration. The engine refuses to publish without an
injected writer, and no command constructs one.

## Preserved policy

Constants are preserved from the automation they replace. The source column names
the file whose behavior each constant reproduces.

| Constant | Value | Source |
| --- | --- | --- |
| owned output identity | `aro-hcp-robot[bot]` | `tool_version_guard.py`, `agentic_pr_maintenance.py` |
| tool label | `agentic-tool-upgrade` | `tool_version_guard.py`, `agentic_pr_maintenance.py` |
| tool title prefix | `chore(tools): ` | `tool-version-updates.md`, `tool_version_guard.py` |
| tool family marker | `<!-- agentic-tool-family: … -->` | `tool-version-updates.md` |
| security title and label | `fix(deps): ` / `agentic-dependabot` | `dependabot-remediation.md` |
| native label and marker | `agentic-dependency-upgrade` / `<!-- native-dependabot-maintenance:` | `gh-aw-recompile.md`, continuation workflow |
| hold label | `do-not-merge/hold` | `agentic_pr_maintenance.py` |
| retry-exhaustion hold | `/hold\n\nRevision <sha> was retested N times: holding` from `openshift-merge-bot[bot]` | `agentic_pr_maintenance.py` |
| per-head recovery marker | `gh-aw-recompile:recovery:<sha>` | `agentic_pr_maintenance.py` |
| required checks for hold release | `ci/pipeline-gate`, `ci/prow/e2e-parallel` | `agentic_pr_maintenance.py` |
| tool throughput | at most two family attempts, one proposal per run, one open owned tool proposal on `main` | `tool-version-updates.md` |
| security throughput | at most six groups, one proposal per manifest, 20 reconciles plus one repair, 24-hour retest cooldown | `dependabot-remediation.md`, `dependabot_guard.py` |
| native throughput | one family and one lifecycle action per run | `gh-aw-recompile.md` |
| public run evidence | 64 MiB per artifact, 60-second timeout, `finished.json` and `build-log.txt`, manifest published last | `fetch_prow_logs.py` |

Tool ownership requires the author, head and base repository, base ref and the
profile markers to agree. A title or label alone never confers mutation
authority. CI evidence requires positive results at the exact current head; a
missing, pending, old-head, neutral or skipped result is never positive.

## Decisions

Defaults are local and fail closed. A decision marked as blocking a phase stays
open until that phase is reviewed.

| ID | Question | Current answer | Blocks |
| --- | --- | --- | --- |
| D01 | Naming, module, configuration | `tooling/depctl`, workspace member, Cobra CLI, compiled policy defaults, no configuration file | interface freeze |
| D02 | Tool ranking | explicit `--target` only; no oldest-pin ordering | automatic selection |
| D03 | Publisher and head repository | not selected; publication requires an injected writer | live publication |
| D04 | Authority matrix | local Git, public Prow and `gh api` reads only; no write authority registered | live read and write scopes |
| D05 | Lifecycle owner | existing workflows remain the only producers | scheduling cutover |
| D06 | Execution integration | local CLI only; adapters injected by the caller | hosted pilot |
| D07 | Coordination | single-host journal lock, insufficient for multiple publishers | multiple live runners |
| D08 | Agent backend | none; repair is a phase that reports repair-required | real repair |
| D09 | Optional scopes | Docker replacement, runtime and toolchain upgrades, images: unsupported | images and runtime work |
| D10 | Contract limits | schema `depctl/v1`, strict decoding 16 MiB, logs 2 MiB, evidence 64 MiB, exit codes 0/2/3/4/5/6 | caller adoption |
| D11 | Security repair boundary | unchanged; the inline repair shell is untouched | security parity |
| D12 | Distribution and platforms | local `linux/amd64` builds with ldflags provenance; no release pipeline | distribution |

## Architecture

```text
tooling/depctl/
  main.go             cmd/                      thin Cobra adapters
  pkg/depctl/         public exit contract
  internal/schema/    versioned envelope, canonical JSON, strict decoding
  internal/model/     typed records
  internal/semver/    SemVer precedence
  internal/discovery/ pin discovery and upstream resolution
  internal/policy/    pure tools and maintenance decisions
  internal/repository/ hook-free Git plumbing
  internal/workspace/ isolated candidate worktree and typed edits
  internal/consumers/ fixed consumer registry and bounded runner
  internal/validation/ baseline and candidate comparison
  internal/state/     journal, named state, single-host lock
  internal/forge/     forge interfaces, fake, read-only gh adapter
  internal/evidence/  public run artifact retrieval
  internal/orchestration/ lifecycle, publication, maintenance, investigation
  internal/testkit/   fixture repositories, fake services, scenarios
```

Dependencies point from commands to orchestration to pure policy. Policy loads no
credentials, runs no process, reads no environment and performs no I/O.

## Lifecycle

```text
discover -> plan -> prepare_candidate -> validate -> proposal_ready
        -> await_authorization -> gate -> publish -> observe_ci -> maintain -> completed

Terminal outcomes: no_change | waiting | blocked | stale | failed | interrupted
```

A local tools run stops at `await_authorization`. The publication half of the
state machine runs only with an injected writer, which the CLI never supplies.

## Trust boundaries

- Plans name fixed consumer IDs with typed parameters; no record carries a command.
- Candidates are detached worktrees. A source checkout whose base moved, or whose
  planned files have uncommitted edits, is refused as stale.
- Native generation must converge; a repeated pass that changes the tree fails the
  candidate.
- Publication re-reads the single-flight slot, the expected base and the observed
  identity, and reconciles a lost branch or creation response instead of retrying
  blindly.
- When a journal is configured, publication records intent before the effect and
  the observation afterwards. The lock is single-host only.
- Failure investigation reports `candidate_regression`, `supported_unrelated_failure`
  or `unknown_incomplete`. A retrieval failure is never a diagnosis, and a
  consumed per-head budget stays consumed.

## Validation layers

`depctl` tests and generated-candidate consumers are separate layers and both run.
Consumer verdicts are `validated`, `candidate_regression`, `baseline_blocked` and
`incomplete`. A missing prerequisite is incomplete, never a pass.
