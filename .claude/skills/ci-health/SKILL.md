---
name: ci-health
description: Fleet-wide CI health scan with infra event detection and fleet-wide failure classification
user-invocable: true
---

## Tool

```bash
tooling/ci-triage/ci-triage summary --since 14d
```

## Output Fields

```json
{
  "envs": [{
    "env": "prod",
    "total": 72, "passed": 21, "failed": 51,
    "pass_rate": 0.29,
    "infra_failures": 5,
    "flaky_tests": ["TestA", "TestB"],
    "top_failures": ["TestX", "TestY"]
  }],
  "fleet_failures": [{
    "test": "TestX",
    "envs": ["int", "prod", "stg"],
    "classification": {"class": "fleet_wide", "confidence": "high", "reason": "..."}
  }]
}
```

## How to Read

### Pass rates
- Below 0.3 = critical, environment is broken
- 0.3-0.6 = degraded, persistent regressions
- 0.6-0.8 = marginal, some failures
- Above 0.8 = healthy, residual flakes

### `infra_failures` — infrastructure events (wipeouts)
These are jobs where >80% of tests failed OR Sippy flagged as infrastructure failure. They represent infrastructure events (provisioning failure, capacity, networking), NOT test regressions. Subtract from failure count to get the real regression signal.

### `flaky_tests` — known flakes
Tests that Sippy recorded as flaking (passed on retry). Lower priority than consistent failures.

### `fleet_failures` — tests failing across environments
Each has a `classification`. `class: fleet_wide` with `confidence: high` means a test fails in 2+ environments simultaneously — usually a code change, though shared infrastructure issues (DNS, certs, Azure region) can also cause fleet-wide failures.

### Fleet pattern interpretation
- Fails in ALL envs simultaneously → code change or shared dependency
- Fails in int + stg but not prod → recent code not yet deployed to prod
- Fails in prod only → prod-specific config, scale, or deployment timing
- Fails in one non-prod env only → environment health

## Next Steps

- Environment looks broken → `/ci-investigate ENV`
- Fleet-wide failure → check `onset_rollout` via `/ci-correlate ENV`
- Want a full answer → `/ci-diagnose ENV`
- Everything green but PR failing → `/ci-triage-pr NUMBER`
