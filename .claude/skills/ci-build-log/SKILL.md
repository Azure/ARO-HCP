---
name: ci-build-log
description: Causation layer — build logs, Azure API logs, step timing, test output
argument-hint: <url> <env> | test-detail <url> <env> <test>
user-invocable: true
---

## Tools — Three Levels of Depth

```bash
# Level 1: CI step execution log (job-level)
tooling/ci-triage/ci-triage build-log URL ENV --lines 200
tooling/ci-triage/ci-triage build-log URL ENV --step provision --lines 200

# Level 2: Per-test deep dive (full error + output + Azure API logs)
tooling/ci-triage/ci-triage test-detail URL ENV "Exact Test Name"

# Level 3: Automated investigation (adds step timing + cross-CI scope)
tooling/ci-triage/ci-triage investigate ENV --test "Test Name" --since 14d
```

## When to Use Which

| Scenario | Tool | Why |
|----------|------|-----|
| All tests failed (wipeout) | `build-log --step provision` | Check provisioning |
| One test times out | `test-detail` | Azure log shows which API was slow |
| Job killed externally | `build-log` | Shows "signal: killed" |
| ARM API errors (403, 429, 500) | `test-detail` | Azure log has request/response |
| Want automated analysis | `investigate --test` | Chains all sources + cross-CI |

## test-detail Output

```json
{
  "test": "Customer should ...",
  "result": "failed",
  "duration": 2549191,
  "start_time": "2026-04-10 20:31:25 UTC",
  "end_time": "2026-04-10 21:13:54 UTC",
  "error": "fail [file.go:174]: timeout '10' minutes exceeded...",
  "output": "\"ts\"=\"2026-04-10 20:31:25\" \"msg\"=\"creating resource group\"...",
  "azure_log": [
    {"time": "2026-04-10T20:31:33Z", "level": "INFO", "msg": "...", "event": "LongRunningOperation"}
  ]
}
```

## Azure API Log Analysis

`azure_log` entries have these event types:

| Event | Meaning |
|-------|---------|
| `Retry` | Starting an HTTP request (includes Try=N) |
| `Request` | Full outgoing request details |
| `Response` | HTTP response received |
| `LongRunningOperation` | ARM async operation polling |
| `ResponseError` | Non-2xx response — **the error events** |

### LRO State Transitions
ARM async operations progress: `Accepted` → `Running` → `Provisioning` → `Succeeded`/`Failed`

Key patterns:
- Many `Accepted` + few `Succeeded` = operations stuck in queue
- Long `Provisioning` = ARM is working but slow
- `State Failed` = ARM operation failed (check ResponseError)

### Timing Gap Analysis
Gaps between entries = blocked time. A 30-minute gap between a PUT and the next log line means something was blocked for 30 minutes.

### Common Azure Patterns

| Pattern | Likely Cause |
|---------|-------------|
| Long gap → "context deadline exceeded" | Upstream dependency slow or unresponsive |
| "signal: killed" | Prow job timeout (total time exceeded) |
| Repeated `Try=2`, `Try=3` | Transient errors triggering retries |
| HTTP 429 responses | Azure rate limiting |
| HTTP 5xx responses | Azure service error |
| `LongRunningOperation delay for 10s` (many times) | Polling a stuck or slow operation |

These are starting hypotheses — verify against the actual log context before concluding.

## Build Log Analysis

Read timestamps. The GAP between timestamps is where the time went. Read BACKWARDS from the failure to find the FIRST thing that went wrong.

Check provision step first for wipeouts. A timeout in the test step might be because provisioning consumed the time budget.

## Next Steps

- Found slow ARM operation → check if fleet-wide via `/ci-investigate ENV`
- Found provisioning failure → check other jobs at same time
- Need code correlation → `/ci-correlate ENV`
