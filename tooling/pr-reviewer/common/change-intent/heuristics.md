# Change Intent Heuristics

Classify the change before reviewing it.

## Common intents

- **behavior change** — validation logic, controller logic, retry policy, API semantics
- **generated sync** — deepcopy, SDK, OpenAPI, rendered config, Helm fixtures
- **config / rollout change** — config, topology, pipeline, Bicep, image digests
- **refactor** — code movement or cleanup with intended behavior preservation
- **test-only** — tests or verifiers change without product code

## Review implications

- Generated-sync PRs should be judged on source-of-truth alignment, not style.
- Refactors still need scrutiny if they touch shared packages or state transitions.
- Test-only changes deserve attention when they weaken an invariant.
- “Automated” or image-digest PRs are usually lower risk but should still be checked for unintended config drift.
