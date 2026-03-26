# PR #4318 — Removing generic timeout retries

- **Title:** `Remove "context deadline exceeded" retries`
- **Merged:** 2026-03-05
- **Touched areas:** many `pipeline.yaml` files across `acm/`, `admin/`, `backend/`, `cluster-service/`, `dev-infrastructure/`, `frontend/`, `hypershiftoperator/`, `maestro/`, `pko/`, `route-monitor-operator/`, `secret-sync-controller/`

## Why it mattered

The patch was mechanically small but operationally large. The author rationale was that `context deadline exceeded` was too generic to retry against safely.

## Reusable lesson

When a PR changes retry policy across many pipeline files, the reviewer should treat it as high blast radius even if the textual diff is tiny. The core question is whether the retry signature is specific to a transient failure mode or too generic and likely to hide real defects.
