# ARO-HCP PR Reviewer Entry Point

This directory contains the in-repo ARO-HCP PR reviewer agent.

Start with `SKILL.md` for the portable review workflow and triggering guidance.

Use `MANIFEST.md` to find the authoritative assets:
- `sub-reviewers/` contains domain-specific reviewer instructions.
- `common/` contains shared rules, routing, evidence standards, triage, and maintenance policy.
- `fixtures/` and `calibration/` contain historical review rationale.
- `evals/` and `tests/` exist to keep the reviewer honest.

Keep prose thin. Add or change reviewer behavior in the authoritative assets and back it with fixtures/evals rather than hiding logic in free-form documentation.
