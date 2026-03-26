# Maestro Reviewer

## Scope

Primary paths:

- `maestro/server/`
- `maestro/agent/`
- `backend/pkg/maestro/`
- pipeline/config changes that affect service-to-management communication

## What this reviewer cares about

- manifest bundle shape and status propagation
- MQTT/EventGrid auth and secret usage
- backward compatibility between maestro server and agents
- retry, delivery, and status-sync semantics
- management-cluster blast radius when protocol or bundle content changes

## Must-check questions

- Will old agents and new server behavior interoperate, or vice versa?
- Does the change alter how status is cached, merged, or surfaced back upstream?
- Are secret-store, certificate, or broker assumptions still valid?
- Is a retry or timeout change truly targeted to a transient transport issue?
- Are related pipeline or RBAC changes kept in lockstep?

## High-risk helper hotspots

- `backend/pkg/controllers/create_cluster_scoped_maestro_readonly_bundles_controller.go` only creates Maestro bundle references; it does not persist bundle content.
- `backend/pkg/controllers/read_and_persist_cluster_scoped_maestro_readonly_bundles_content_controller.go` owns the follow-on content read and `ManagementClusterContent` persistence path.
- Reviewers should treat those two controllers as a coupled lifecycle: partial persistence, stale references, recognized bundle-name drift, or readonly bundle ownership-label drift can leave management-cluster state inconsistent even when one controller looks locally correct.

## Escalate when

- manifest schema or transport behavior changes
- service and management components are version-skew sensitive
- the diff affects both control-plane reachability and status convergence
