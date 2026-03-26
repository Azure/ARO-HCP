# Coverage Model

Coverage is not just test count. Track:

- which domains have seeded historical fixtures
- which change archetypes have eval prompts, counted from the explicit `domains` field in `evals/evals.json`
- how recent the history seed is per domain
- whether the domain has clear ownership and escalation guidance

Gaps in any of these should show up in `common/coverage/seed-coverage.json`.
