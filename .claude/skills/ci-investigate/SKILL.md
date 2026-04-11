---
name: ci-investigate
description: Deep failure investigation — classification, cross-env context, onset, deployment, messages
argument-hint: <env>
user-invocable: true
---

## Tools

```bash
# Classified failure groups with cross-env context, onset, deployment, messages
tooling/ci-triage/ci-triage failures ENV --since 14d

# Deep single-test investigation: adds GCS artifacts + cross-CI scope
tooling/ci-triage/ci-triage investigate ENV --test "Test Name" --since 14d

# One test in one job: full error + output + Azure API logs
tooling/ci-triage/ci-triage test-detail JOB_URL ENV "Test Name"

# Time-series with rollout and infra flags
tooling/ci-triage/ci-triage timeline ENV --since 14d
```

## Output Fields — `failures`

Each failure group now includes:

```json
{
  "test": "Customer should ...",
  "count": 21,
  "classification": {
    "class": "regression",       // regression | flaky | infrastructure | fleet_wide | unknown
    "confidence": "high",        // high | medium | low
    "reason": "deterministic failure with onset, 100% failure rate in non-infra runs"
  },
  "other_envs": ["stg", "prod"],  // same test failing elsewhere
  "onset_rollout": {               // EV2 deployment running at onset
    "commit": "abc123",
    "build": "20260407.1",
    "region": "uksouth"
  },
  "last_passed": "2026-04-04T02:54:48Z",
  "first_seen": "2026-04-05T02:00:47Z",
  "messages": [{"msg": "fail [file.go:174]: ...", "count": 15}],
  "jobs": ["https://prow.ci.openshift.org/..."]
}
```

Top-level also includes:
- `infra_jobs`: infrastructure events separated out (with classification)
- `fleet_context`: `{"stg": 0.4, "prod": 0.29}` — pass rates of other envs
- `rollout`: the most recent EV2 deployment info

## The Reasoning Chain

### 1. Classification tells you WHAT kind of problem

| Class | Meaning | Action |
|-------|---------|--------|
| `regression` | Deterministic, has onset | Find what changed at onset |
| `fleet_wide` | Failing in 2+ envs | Code change, not infra |
| `flaky` | <50% failure rate | Lower priority unless trending up |
| `infrastructure` | Mostly in wipeout jobs | Infra event, not test bug |
| `unknown` | Insufficient data | Need more investigation |

### 2. Onset + deployment indicates WHEN and WHICH code

`last_passed` → `first_seen` = the onset window. `onset_rollout` shows which EV2 deployment was running at onset. If `onset_rollout.commit` differs from the prior passing run's rollout, that's a strong deployment correlation — but not proof. Verify by checking what changed in that deployment.

### 3. Cross-env context tells you the SCOPE

`other_envs: ["stg", "prod"]` means the same test fails elsewhere. Combined with `fleet_context` pass rates, this distinguishes:
- All envs degraded → systemic issue (code or shared infra)
- Only this env → environment-specific (config, capacity, certs)

### 4. Messages tell you HOW it manifests

Error messages from extension test results (full, not truncated). Parse the failure mode — but remember these describe the SYMPTOM, not necessarily the cause:
- `timeout 'N' minutes exceeded` → something is slow, stuck, OR the timeout is misconfigured
- `expected 200 got 403` → auth/RBAC issue, OR the API path changed
- `failed waiting for hcpCluster` → cluster didn't reach ready, OR readiness check is wrong
- `Interrupted by User` → job killed by Prow timeout (cascading from earlier delays)

### 5. `investigate` adds GCS deep dive

`investigate ENV --test "Name"` chains failures + GCS artifacts + cross-CI:
- Step timing from ci-operator-step-graph.json
- Azure API logs for the test
- Cross-CI scope check: "is this ARO-specific or platform-wide?"

## Common Traps

- Error messages point at the DETECTING component, not the CAUSING one
- `infra_jobs` are separated from real failures — don't count them as regressions
- `[sig-sippy]` meta-tests are already filtered out by the tool
- Same revision passing and failing = environmental, not code
- **The classification is a hypothesis, not a conclusion.** If you see evidence that contradicts the tool's classification (e.g., classified "flaky" but you notice it fails in every env), trust your reasoning and investigate further

## Next Steps

- Found onset + deployment change → `/ci-correlate ENV` for PR mapping
- Need Azure API details for a test → `test-detail URL ENV "Name"`
- Want cross-CI scope → `search "error" --cross-ci`
- Want full automated verdict → `/ci-diagnose ENV`
- Suspect a PR → `/ci-triage-pr NUMBER`
