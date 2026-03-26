# Lifecycle Operators Reviewer

## Scope

Primary paths:

- `hypershiftoperator/`
- `acm/`
- `route-monitor-operator/`
- `pko/`
- `velero/`
- `secret-sync-controller/`
- `tooling/olm-bundle-repkg/`
- `tooling/secret-sync/`

## What this reviewer cares about

- management-cluster operator behavior and policy rollout
- CRD / manifest compatibility
- RBAC and namespace assumptions in operator deployments
- policy-driven config, cluster addons, and secret propagation
- fixture and helm rendering updates for operator charts

## Must-check questions

- If a CRD, policy, or chart changed, were generated/fixture artifacts updated?
- Are namespace, role, and clusterrole changes least-privilege and still sufficient?
- Does a policy change assume a resource exists that another PR provides?
- Are cleanup or disaster-recovery behaviors preserved when Velero/secret sync changes?

## Escalate when

- CRD versioning or migration safety is unclear
- RBAC broadens without a narrow operational reason
- operator logic depends on timing/order across management-cluster services
