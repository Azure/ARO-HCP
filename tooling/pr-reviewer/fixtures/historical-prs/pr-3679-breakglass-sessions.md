# PR #3679 — Admin API breakglass sessions

- **Title:** `admin api: breakglass sessions`
- **Merged:** 2026-02-26
- **Touched areas:** `admin/server/`, `acm/deploy/helm/policies/`, `config/config.yaml`, `config/rendered/`, `admin/deploy/`, `admin/values.yaml`, `admin/README.md`

## Why it mattered

This PR added a breakglass session surface to the admin API and touched policy, deployment, config, rendered outputs, middleware, and docs together. Reviewers treated it as a security-sensitive rollout where vague field semantics or drift between config, policy, and deployment could create the wrong access model.

## High-signal review moments

- Reviewers asked for required and immutable schema semantics rather than leaving sensitive fields loosely mutable.
- Good comments pushed to derive cluster identity from the HCP resource ID instead of asking users to supply identifiers the system could already infer.
- Reviewers also challenged unclear field documentation and value semantics because an admin surface with ambiguous meaning is an operational and security risk.

## Reusable lesson

For admin/security-sensitive PRs, the reviewer should verify:

- whether required and immutable field constraints are explicit
- whether identity can be derived from trusted existing resource identifiers instead of new user input
- whether config, rendered outputs, policies, deployment templates, and docs stay consistent
- whether a security-sensitive rollout is being reviewed as a multi-surface access change, not just a handler diff
