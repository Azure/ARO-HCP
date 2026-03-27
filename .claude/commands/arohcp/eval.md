---
description: Run ARO-HCP reviewer evals, including the mixed-domain suite
argument-hint: [mixed | all | eval-id[,eval-id...]]
allowed-tools: Read, Glob, Grep, Bash(git:*), Bash(make:*), Bash(python3:*)
---

# ARO-HCP Eval

Use the in-repo eval set to check whether the reviewer is still producing the expected kind of review.

## Process

1. Read `tooling/pr-reviewer/evals/evals.json` if you need context on the selected eval ids.
2. Resolve the eval selection from `$ARGUMENTS`:
   - if empty or `mixed`, use the runner's curated mixed suite
   - if `all`, run the full eval set
   - if numeric ids are provided, run those ids
3. Prefer the shared automated runner:
   - `make -C tooling/pr-reviewer evalcheck` for the default mixed suite
   - `make -C tooling/pr-reviewer evalcheck SELECTION=all` for the full set
   - `make -C tooling/pr-reviewer evalcheck SELECTION="$ARGUMENTS"` for explicit ids
4. The shared runner lives at `tooling/pr-reviewer/common/tools/run_reviewer_evals.py` and is the source of truth for:
    - executing the reviewer from `tooling/pr-reviewer/SKILL.md` and the assets indexed by `tooling/pr-reviewer/MANIFEST.md`
    - executing the reviewer against the selected eval prompts
    - scoring the output with the automated judge
    - deciding pass/fail and surfacing missing behaviors
5. Keep the eval command local to the repo assets and runner; it should not need `gh` access.
6. Return the runner summary and call out which reviewer assets likely need tightening if any eval fails.

## Your Task

Run reviewer evals for `$ARGUMENTS` using the shared automated runner for the in-repo ARO-HCP reviewer assets.

Do not modify files unless the user explicitly asks for prompt or asset tightening after the eval results.
