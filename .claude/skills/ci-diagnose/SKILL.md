---
name: ci-diagnose
description: Full automated synthesis — verdict with confidence and evidence chain
argument-hint: <env>
user-invocable: true
---

## Tool

```bash
# Full diagnosis for an environment
tooling/ci-triage/ci-triage diagnose ENV --since 14d

# Focused on one test
tooling/ci-triage/ci-triage diagnose ENV --test "Test Name" --since 14d
```

## What It Does

Runs the full investigation chain automatically:
1. Fleet health scan (summary)
2. Deep failure investigation with GCS artifacts (investigate)
3. Onset-to-deployment/PR correlation (correlate)
4. Rule-based verdict synthesis

Returns a structured verdict with confidence level and evidence chain.

## Output Fields

```json
{
  "env": "prod",
  "verdict": "regression from deployment abc123..def456, correlated with PR #4724",
  "confidence": "high",
  "evidence": [
    {"fact": "classification: regression with high confidence", "source": "failures"},
    {"fact": "onset: 2026-04-05, last_passed: 2026-04-04", "source": "failures"},
    {"fact": "deployment changed: abc123 → def456", "source": "correlate"},
    {"fact": "PR #4724: remove old operations scanner [deads2k]", "source": "correlate"},
    {"fact": "error: timeout 45 minutes during CreateHCPClusterFromParam", "source": "investigate"},
    {"fact": "LRO stuck in Provisioning for 30+ minutes", "source": "azure_log"}
  ],
  "investigation": { /* full investigate output */ },
  "correlation": { /* full correlate output */ },
  "fleet_health": { /* summary output */ }
}
```

## When to Use

- **Use diagnose** when you want the answer fast — it chains everything automatically
- **Use manual skills** when you need to investigate interactively, follow unexpected leads, or the verdict doesn't make sense

## How to Read the Verdict

The verdict is rule-based synthesis. Read it critically:
- **High confidence** = strong evidence chain, probably correct
- **Medium confidence** = reasonable but check the evidence yourself
- **Low confidence** = insufficient data, use as a starting point not an answer

The `evidence` array shows every fact and its source. If a fact seems wrong, go to that source directly to verify.

## Verdicts the tool can produce

| Pattern | Verdict |
|---------|---------|
| Sippy infra flag + wipeout | "infrastructure event, test failures are collateral damage" |
| Same test in 2+ envs | "fleet-wide failure, also failing in [envs], likely code change" |
| Deployment commit changed at onset | "regression from deployment <commit_range>" |
| Clear onset + PRs in window | "regression correlated with PR #N by Author" |
| <50% failure rate | "flaky — intermittent failures" |
| None of the above | "insufficient data for confident verdict" |

## Traps

- The rule-based synthesis may miss nuance. If the verdict says "infrastructure" but you see fleet-wide patterns, investigate manually.
- The verdict chains data from multiple API calls. If one call returned incomplete data (e.g., Sippy timeout), the verdict may be wrong.
- For the hard 30% (race conditions, multi-component issues), the verdict will say "insufficient data" — that's honest, not a failure.
