# depctl design

This document describes the model `depctl` implements and the constraints that
shape it. Command usage lives in the [README](../README.md); preserved constants
and open decisions live in [contract.md](contract.md).

## Purpose

Dependency automation was split between workflow prose and helpers that ran only
inside a hosted workflow. Policy could not be exercised locally, candidates were
validated after publication, and failure investigation depended on whatever
evidence the workflow happened to collect.

`depctl` moves deterministic policy and orchestration into one Go module so the
same code serves a local CLI, deterministic simulation and, later, an approved
execution integration. A scheduler may supply triggers, capabilities and
notifications; it must not reproduce grouping, selection, ownership or
publication policy.

Non-goals: a general shell framework, a deployment controller, a merge-policy
bypass, self-approval, automatic removal of human holds, and replacements for
package managers or generators with text substitutions.

## Model

A **profile** selects policy. This build implements `tools`; security, native
Dependabot, compiler and image profiles exist only as preserved constants and
decisions.

A **family** is one upstream dependency: a Go module path or an upstream
repository. Its **pins** are the repository locations that carry the version
(`.bingo/<tool>.mod`, a `versions.mk` variable and the matching Dockerfile
argument). A family is `current`, `outdated`, `ahead`, `blocked` or `error`; a
pin that cannot be read makes the family an error, never current.

A **plan** selects at most one family per preparation, records the exact target,
the base revision and the fixed consumers, and is identified by canonical content
without timestamps or run IDs.

## Lifecycle

```text
discover -> plan -> prepare_candidate -> validate -> proposal_ready
        -> await_authorization -> gate -> publish -> observe_ci -> maintain -> completed
```

Terminal outcomes are `no_change`, `waiting`, `blocked`, `stale`, `failed`,
`interrupted`, `validation_failed`, `prepared`, `awaiting_authorization` and
`completed`. A phase reports `complete` when it collected the evidence its
declared outcome needs, not when an upgrade merged. Version state, phase and CI
state are separate fields; a prepared candidate is not a delivered upgrade.

A local run stops at `await_authorization`. The publication half of the machine
runs only when a writer is injected, which the CLI never does. Publication
re-reads the single-flight slot, the expected base and the observed identity, and
reconciles a lost response rather than retrying blindly. When a journal is
configured, the intent is recorded before an effect and the observation after it.
The journal lock is single-host only and is not a distributed lease.

## Records and identity

Every record carries `schemaVersion: depctl/v1`, a kind and bounded fields;
decoding rejects unknown fields, trailing content and oversized input. Digests
are computed over a canonical encoding with sorted object keys.

- **Policy digest** covers the compiled limits, identities, required checks and
  the registered consumers, including each consumer's program, arguments,
  directory and environment overrides. A plan is bound to the code that will run
  its gates, not only to their IDs.
- **Plan identity** covers repository, base, profile, policy digest, sorted
  targets, deferred families, consumers and outcome. It excludes timestamps and
  run IDs so a retry is the same logical plan.
- **Candidate identity** covers the plan, base, materialized tree digest, patch
  digest, changed paths and the candidate commit.
- **Validation** is bound to a plan and candidate; **proposal** additionally
  binds the deterministic group and branch.

A digest proves consistency, never authority. A decoded record can carry its own
hash, so every consumer of a record revalidates it against compiled policy and
the repository.

## Trust boundaries

Planning and preparation treat every decoded record as untrusted input.

- The plan is revalidated before any work: edit kinds, edit sites, target
  values, the consumer set, the policy digest and the plan identity are
  recomputed. A record cannot add, drop, redirect or substitute a gate.
- Release gates come from the upstream repository that a CI variable tracks, and
  a family must match that repository. The recorded baseline must equal the
  current `versions.mk` value and the Dockerfile argument; a downgrade is
  refused.
- Edit sites are confined to the candidate: canonical relative paths only, with
  symlinked target files and symlinked parents rejected, the destination resolved
  before any directory is created, and any directory created for a refused
  candidate removed.
- Target values are plain versions. Line terminators, Make syntax and shell
  metacharacters are refused at the apply boundary as well as in planning.
- Consumer commands come from a fixed registry. A plan names an ID and typed
  parameters; no record carries a command.
- A candidate record must belong to the same plan and base, its change set must
  stay within the plan's scope, and every planned target file must actually
  carry the planned value. Matching self-supplied hashes are not enough.
- Candidate bytes, the committed revision and the candidate identity are
  rechecked before verification and before packaging. A worktree edited after
  apply is drift, and a consumer that rewrites tracked files is drift rather than
  validation.
- Packaging recomputes the verdict from the recorded results and refuses an
  incomplete, contradictory or duplicate consumer/phase result set.
- Publication accepts only the committed candidate revision, pushes it to a
  branch at the expected head, and re-reads the result. A proposal observed at a
  different revision is stale.

## Policy and adapters

Policy is pure: no credentials, no processes, no network, no environment. It
decides which families are eligible, which consumers are required, whether a
hold may be released and what a failure means.

Adapters perform effects: Git plumbing without repository hooks, consumer
execution, forge reads, public artifact retrieval. Candidate execution and
trusted publication are separate stages with separate capabilities.

Consumers run with Make and Git control variables removed, because an exported
`MAKEFLAGS` or `BICEP` could change what executes or which compiler runs. A
consumer that needs a specific executable verifies its reported version first;
an exact binary can be selected per phase through the consumer's own mechanism
(`BICEP` for the Bicep Make target, a name alias on `PATH` for the promtool
lookup), and the launcher is never replaced. Consumers run in their own process
group so cancellation reaches descendants, with a bounded wait.

Native generation must converge: a repeated pass may not change the materialized
tree. Digests are derived from the committed tree so a newly generated file keeps
one representation.

## Failure investigation

Investigation answers one question: is this failure caused by the candidate,
supported as unrelated, or not yet known.

1. Evidence is collected per failing check from its exact public run URL:
   `finished.json` and `build-log.txt`, within size and time bounds, with the
   manifest published last and existing outputs refused.
2. Each retrieved run must record the assessed head as its revision. A run
   without a recorded revision is incomplete evidence.
3. Each failing check is diagnosed on its own log. A comparison run is accepted
   only when it belongs to the same job and path, records its own revision that
   differs from the assessed head, succeeded, and contains that check's failure
   signature.
4. A candidate regression in any failing check takes precedence. Supported
   unrelated failure requires an accepted comparison for every failing check.
   Anything else is `unknown_incomplete`, with the missing evidence enumerated.
5. Diagnosis never widens policy. A human or unexplained hold, a failing or
   pending current head, a consumed per-head recovery budget or missing evidence
   keeps the decision blocked.

## Invariants

| Invariant | Enforced by | Covered in tests |
| --- | --- | --- |
| No source checkout modification | path confinement, stale guards, external scratch directories | workspace, testkit containment and stale-source tests |
| No unvalidated gate set | plan revalidation, consumer derivation, policy digest | plan-rejection and stripped-consumer tests |
| No untrusted command execution | fixed registry, typed parameters, environment sanitization | consumer registry and `MAKEFLAGS` tests |
| No published path without validation | verdict recomputation, duplicate rejection, commit binding | packaging and publication tests |
| Deterministic identity | canonical encoding, digest composition | schema and plan-identity tests |
| Convergent generation | materialized-tree digest, every generator repeated | nonconvergent-generator test |
| Preserved holds and budgets | hold replay, recovery eligibility | maintenance policy tests |
| Evidence, not confidence | revision binding, comparison provenance, per-check diagnosis | evidence and maintenance tests |

## Deferred capabilities

Publication, hold release, retest, compiler refresh, scheduling and shared
coordination are not implemented; their prerequisites are recorded as decisions
in [contract.md](contract.md). The security, native Dependabot, compiler and
image profiles are preserved in the contract but not ported. Repair is modelled
as a phase that reports `repair-required`; no model provider is selected.
