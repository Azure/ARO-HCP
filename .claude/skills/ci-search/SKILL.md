---
name: ci-search
description: Cross-CI failure search — is this error ARO-specific or platform-wide?
argument-hint: <error-string>
user-invocable: true
---

## What This Reveals

Searches for a failure pattern across all OpenShift CI jobs (not just ARO-HCP).
Answers the critical question: "is this error happening only in our jobs, or is
all of OpenShift CI seeing it?"

This uses search.dptools.openshift.org which indexes ~108GB of JUnit failures
and build logs across ~307K jobs.

## Tools

```bash
# Search ARO-HCP jobs for an error pattern
tooling/ci-triage/ci-triage search "context deadline exceeded" --max-age 48h

# Search build logs instead of JUnit
tooling/ci-triage/ci-triage search "error pattern" --type build-log

# Compare ARO vs all of OpenShift CI
tooling/ci-triage/ci-triage search "error pattern" --cross-ci
```

## How to Read the Data

### Cross-CI scope (`--cross-ci`)
Returns:
- `aro_specific`: true if the error only appears in ARO-HCP jobs
- `aro_matches`: number of matches in ARO-HCP jobs
- `all_matches`: number of matches across all OpenShift CI
- `assessment`: human-readable interpretation

### What scope tells you about causation
- **ARO-specific** → the issue is in ARO-HCP code, config, or infrastructure
- **Platform-wide** → the issue is in a shared OpenShift component, base image,
  or CI infrastructure — check upstream, not our code
- **Mostly ARO** → might be an ARO-specific interaction with a shared component

## When to Use This

1. **After identifying a failure message** from `/ci-investigate` — check if it's unique to us
2. **When seeing unfamiliar errors** — quick check if this is a known platform issue
3. **When a failure suddenly appears** — check if all of CI broke at the same time
4. **Before deep investigation** — if it's platform-wide, investigation targets change

## Common Traps

- Search is regex-based (ripgrep). Escape special characters in error messages.
- The `--max-age` flag uses Go duration format: `48h`, `168h` (not `2d`, `7d`).
- Long, specific error messages match fewer jobs. Truncate to the key pattern.
- "No matches" might mean the error is too specific or too recent to be indexed.
