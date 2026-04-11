---
name: ci-triage-pr
description: PR regression triage — baseline comparison, changed files, code correlation
argument-hint: <pr-number>
user-invocable: true
---

## Tool

```bash
# PR analysis with baseline + PR metadata + changed files
tooling/ci-triage/ci-triage pr NUMBER

# PR diff for code correlation
gh pr diff NUMBER
```

## Output Fields

```json
{
  "pr": 4724,
  "title": "remove old operations scanner",
  "author": "deads2k",
  "merged_at": "2026-04-04T01:11:58Z",
  "changed_files": ["backend/oldoperationscanner/operations_scanner.go", "..."],
  "envs": [{
    "env": "int",
    "total": 3, "passed": 1, "failed": 2,
    "has_baseline": true,
    "failures": [{
      "test": "TestX",
      "count": 2,
      "baseline": false,  // [NEW] — not in periodic baseline
      "messages": [{"msg": "fail [file.go:84]: ...", "count": 2}],
      "jobs": ["..."]
    }]
  }]
}
```

## How to Reason

### 1. Separate [baseline] from [NEW]
- `baseline: true` = this failure exists in periodic jobs. Predates this PR.
- `baseline: false` = [NEW]. Not in recent periodic runs. May be a regression from this PR.

### 2. Connect [NEW] failures to changed_files
The output includes `changed_files` from the PR. For each [NEW] failure:
- Test name → component (e.g., `TestARMCreateCluster` → cluster creation)
- Changed files → component (e.g., `backend/oldoperationscanner/` → backend operations)
- Match = strong regression signal

### 3. Read the error in context of the diff
Error says WHAT broke. Diff says WHAT changed. Together = WHY.

### 4. Rule out infrastructure
If ALL tests are [NEW], it's almost certainly infrastructure. Check `/ci-investigate ENV`.

### 5. Check concurrent merges
A [NEW] failure might be from another PR merged around the same time:
```bash
gh pr list --repo Azure/ARO-HCP --state merged --limit 10 --json number,title,mergedAt
```

## Traps
- [NEW] ≠ caused by this PR. Another PR may have merged first.
- `has_baseline: false` = no periodic data (e.g., dev). Classification unreliable.
- Infrastructure failures appear [NEW] because they're transient.
