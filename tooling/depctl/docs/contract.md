# depctl contract

Durable constraints, preserved policy and decisions for `depctl`. The model and
its invariants are in [design.md](design.md); usage is in the
[README](../README.md). Baseline deltas, remote pull-request snapshots and
executed-command reports are not kept in the repository.

## Supported scope

The tools profile is implemented: offline and online pin inventory, planning with
explicit targets, isolated candidate preparation with native generation,
baseline and candidate consumer validation, proposal packaging, and read-only
maintenance investigation.

Publication, hold release, retest, compiler refresh and scheduling require an
authorized execution integration. The engine refuses to publish without an
injected writer, and no command constructs one.

## Preserved policy

Constants are preserved from the automation they replace. The source column names
the file whose behavior each constant reproduces, so a change here is a policy
change and not an implementation detail.

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
| tool throughput | at most two eligible family attempts, one family prepared per run, one proposal per run, one open owned tool proposal on `main` | `tool-version-updates.md` |
| security throughput | at most six groups, one proposal per manifest, 20 reconciles plus one repair, 24-hour retest cooldown | `dependabot-remediation.md`, `dependabot_guard.py` |
| native throughput | one family and one lifecycle action per run | `gh-aw-recompile.md` |
| public run evidence | 64 MiB per artifact, 60-second timeout, `finished.json` and `build-log.txt`, manifest published last | `fetch_prow_logs.py` |

Tool ownership requires the author, head and base repository, base ref and the
profile markers to agree. A title or label alone never confers mutation
authority. CI evidence requires positive results at the exact current head; a
missing, pending, old-head, neutral or skipped result is never positive.

## Decisions

Defaults are local and fail closed. A decision that blocks a phase stays open
until that phase is reviewed.

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

## Deferred capabilities

- Live publication, hold release, retest and compiler refresh are not registered;
  D03 to D07 gate them.
- Shared coordination across hosts is not implemented; the journal lock is
  single-host only.
- Resume and restart recovery after an interrupted phase are not implemented. An
  interrupted run reports `interrupted` and requires reconciliation against
  remote truth.
- The security, native Dependabot, compiler and image profiles are preserved as
  constants and decisions but not ported.
- Bounded repair has no provider; the phase reports `repair-required`.
- Docker replacement, runtime and toolchain upgrades remain unsupported.

## Changing this document

A constant change is a policy change: update the value, its source, the code and
the tests in the same reviewed change. A resolved decision moves to its
implementing change and states what replaces it; unresolved decisions are never
silently relaxed. `design.md` explains the resulting model, and the README only
describes behavior that exists.
