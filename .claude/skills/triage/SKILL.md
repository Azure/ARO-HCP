---
name: triage
description: Triage Prow CI e2e test failures for ARO-HCP environments
argument-hint: <env>  |  pr <number>
user-invocable: true
---

Investigate CI failures. Find root causes from evidence, not error names.

`$ARGUMENTS` is `ENV` (e.g., `int`, `stg`) or `pr NUMBER`. dev has no periodic.

## Tools

The Go binary must be built first if not already available:
```bash
cd tooling/ci-triage && make build && cd -
```

All query commands automatically fetch fresh data from GCS before analyzing. No manual ingest step needed.

```bash
tooling/ci-triage/ci-triage summary [--since 7d] [--json]
tooling/ci-triage/ci-triage failures ENV [--since 14d] [--json]
tooling/ci-triage/ci-triage pr NUMBER [--json]
tooling/ci-triage/ci-triage build-log BASE_URL ENV [--lines 200] [--step provision]
gh pr list --state merged --limit 20 --json number,title,mergedAt,files
gh pr view NUMBER --json title,body,files,author
```

## What the tools reveal and hide

- **summary** reveals which envs are broken and how badly, plus fleet-wide failures that appear across multiple envs. Hides why.
- **failures ENV** reveals what is failing, when it started, and how often. Hides why — error messages name the operation that *detected* the failure, not the one that *caused* it.
- **pr NUMBER** reveals which tests failed for a specific PR and whether each failure exists in periodic baselines (`baseline`) or is new to this PR (`NEW`). Use this instead of manually cross-referencing gh checks with periodic data.
- **build-log** reveals what actually happened — step timestamps, where the test stalled, external kills. **Only tool that shows causation.**
- **gh** reveals what code changed and when. Shows correlation, never causation.

## Traps

- Error messages point at the wrong component. A downstream timeout fires first and gets recorded, but the bottleneck was upstream. Don't diagnose timeout failures without checking build-log timestamps.
- Same revision producing wipeouts and clean runs is telling you about the environment, not the code.
- Merge correlation is not causation. Check periodic on the same revision first.
- One wipeout job inflates failure counts. It's one event, not 30 separate problems.

## Limits

You have: test results, error messages, job timelines, PR history. You do NOT have: backend logs, Azure resource health, cluster-service state. Say what you can see, what you can't, and ask the human a specific question.

## PR triage (`/triage pr NUMBER`)

1. Run `tooling/ci-triage/ci-triage pr NUMBER` — it auto-ingests 14d of data, then analyzes.
2. Failures tagged `[baseline]` exist in periodic — they predate this PR. Failures tagged `[NEW]` don't appear in periodic and may be regressions.
3. Use `gh pr view NUMBER --json files` to correlate NEW failures with changed files.
4. If `has_baseline` is false for an env (e.g. dev), baseline classification is unavailable.

## Output

```
## CI Health: {RED|YELLOW|GREEN}
{1-2 sentence summary}

### {Problem} [{confirmed|indicated|suspected|needs investigation}]
- **Evidence**: {what you observed}
- **Root cause**: {what evidence supports, or "undetermined — needs X"}
- **Action**: {next step}

## Cannot Determine
{Specific questions for human with deeper access}
```
