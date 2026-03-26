# PR #1766 — Billing documents in Cosmos DB

- **Title:** `Add billing documents to Cosmos DB`
- **Merged:** 2025-06-11
- **Touched areas:** `backend/`, `internal/database/`, `internal/mocks/`, `dev-infrastructure/modules/rp-cosmos.bicep`

## Why it mattered

The PR introduced billing-document persistence and backend wiring around Cosmos DB. The risky part was not just whether the new document could be written, but whether the persisted identity and query semantics were explicit enough to avoid silent duplicate-state, stale billing, or future coupling to an accidentally vague key.

## High-signal review moments

- Reviewers asked what the real primary key was and whether the stored identity should be expressed as explicit fields such as managed resource group name and subscription ID rather than a vague composite value.
- Reviewers pushed back on error handling that tried to predict specific failure modes instead of surfacing the returned error directly.
- Reviewers asked what should happen if the query matched more than one document and whether deletion timestamps could be skipped in ways that left billing active for deleted clusters.

## Reusable lesson

For backend/state PRs that introduce or reshape persisted documents, the reviewer should check:

- whether identity/key semantics are explicit and well named
- whether query-cardinality invariants are enforced, especially for duplicate matches
- whether error handling is future-proof instead of clever around current implementation details
- whether delete and stale-document lifecycle behavior could silently keep state or billing alive
- whether tests cover duplicate, missing, and deletion paths rather than only the happy path
