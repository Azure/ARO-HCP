# Blast Radius Model

Score risk by asking how many of these surfaces the change touches:

1. customer-visible API behavior
2. persisted state or migration compatibility
3. shared packages in `internal/`
4. generated artifact families
5. service-cluster + management-cluster coordination
6. infra scope (regional / geography / global)
7. security, RBAC, identities, or secret handling
8. rollout automation / pipeline retries

Heuristic:

- 1-2 surfaces: likely medium unless clearly dangerous
- 3-4 surfaces: review as high-risk by default
- 5+ surfaces: assume cross-cutting escalation is needed unless the change is purely mechanical and the generated artifacts prove it
