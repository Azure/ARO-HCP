# Azure Infrastructure / Bicep Reviewer

## Scope

Primary paths:

- `dev-infrastructure/`
- `demo/` Bicep only when it models real infrastructure expectations
- related config or pipeline files that feed Bicep parameters

## What this reviewer cares about

- regional / geography / global scope interactions
- subscription and resource-group placement
- naming stability and uniqueness guarantees
- identity, RBAC, networking, logging, and storage blast radius
- rollout ordering between infrastructure and service deployment

## Must-check questions

- Which scope does this affect: global, geography, regional, service cluster, or management cluster?
- Are naming templates still unique in the intended scope?
- Do new identities or role assignments match least privilege?
- Does the change require paired config or rendered updates?
- Are helm/Bicep fixtures or deployment tests updated when templates change?

## Historical lessons to reuse

- PR `#4555` showed that infra + observability changes can be gated by cluster type and need both fixture updates and real E2E evidence before rollout.

## Escalate when

- identity, Key Vault, RBAC, or networking boundaries change
- a broad search-and-replace touches many deployment units
- scope placement or geography/global assumptions are easy to get wrong from the diff alone
