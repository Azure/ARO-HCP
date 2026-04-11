---
name: ci-correlate
description: Map failure onsets to EV2 deployments and merged PRs — the bridge from WHEN to WHY
argument-hint: <env>
user-invocable: true
---

## Tool

```bash
# All failures with deployment + PR correlation
tooling/ci-triage/ci-triage correlate ENV --since 14d

# Specific test
tooling/ci-triage/ci-triage correlate ENV --test "Test Name" --window 6h
```

## Output Fields

```json
{
  "env": "int",
  "correlations": [{
    "test": "Customer should ...",
    "last_passed": "2026-04-04T02:54:48Z",
    "first_failed": "2026-04-05T02:00:47Z",
    "onset_window": "2026-04-04 to 2026-04-05",
    "deployment": {
      "last_good_rollout": {"commit": "abc123", "build": "20260404.1", "region": "uksouth"},
      "first_bad_rollout": {"commit": "def456", "build": "20260405.1", "region": "uksouth"},
      "commit_range": "abc123..def456"
    },
    "confidence": "high",
    "confidence_reason": "deployment changed: abc123 → def456",
    "merged_prs": [{
      "number": 4724,
      "title": "remove old operations scanner",
      "author": "deads2k",
      "merged_at": "2026-04-04T01:11:58Z",
      "files": ["backend/oldoperationscanner/operations_scanner.go"],
      "relevance_score": 0.8,
      "relevance_reason": "files touch backend/"
    }]
  }]
}
```

## How to Build a Causal Chain

### 1. Check deployment correlation FIRST

This is the strongest signal. `commit_range` shows exactly which commits deployed between the last passing and first failing run:
```bash
git log abc123..def456 --oneline
```
This is stronger than PR correlation because it accounts for deploy timing (merge ≠ deploy).

### 2. Use confidence scores

| Confidence | Meaning |
|-----------|---------|
| **high** | 1-2 PRs in narrow window, OR deployment commit changed |
| **medium** | 3-5 PRs, or deployment change with wider onset |
| **low** | Many PRs, no deployment change, or wide onset |

### 3. Score PRs by relevance

Each PR has `relevance_score` (0-1) and `relevance_reason`. Higher scores mean the PR's changed files are more likely related to the failing test.

### 4. Verify with the diff

```bash
gh pr diff NUMBER
```
Does the change affect the code path the test exercises? Does it explain the specific error message?

## Cross-env Correlation

If the same test fails in int (onset April 4) and prod (onset April 5), the int→prod progression matches the deployment promotion flow. This confirms a code change propagating through environments.

## Traps

- Merge time ≠ deploy time. EV2 deployment annotations are the truth.
- Wide onset window = many PRs = weak signal. Narrow your `--window`.
- Infrastructure PRs (Bicep, config) can break tests without touching Go code.
- Multiple causes are possible — don't force all failures into one root cause.
