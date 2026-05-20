# Agentic Hints for Debugging ARO HCP With Kusto

- IMPORTANT: this document is referenced by agentic workflows, DO NOT REMOVE IT.
- If any of the info below turns out not to be accurate, suggest to the user an update PR at the end of the session.

## Clusters
- the `hcp-prod-usc` kusto cluster is US canary region and matches prod grafana's `*-eastus2euap` data sources

## Databases

- `HostedControlPlaneLogs` – logs from hosted control plane
- `ServiceLogs` — logs from service and management clusters (underlay)

## Tables

- `containerLogs` — generic container logs from all pods; used for maestro, hypershift, and any service without a dedicated table
- `kubernetesEvents` — K8s events: object state changes, warnings, reasons, source components, first/last seen timestamps
- `frontendLogs` — ARM API request/response logs: HTTP method, path, status code, duration, request/correlation IDs, subscription/resource identifiers
- `backendLogs` — backend service logs: async operations, controller state dumps, cluster lifecycle, error codes, correlation IDs
- `clustersServiceLogs` — Cluster Service logs: cluster provisioning phases, operation IDs, cluster correlation IDs
- `aksEvents` / `kubeAudit` — AKS management cluster events and extracted K8s API audit logs: API verbs, request URIs, user agents, response status
- `systemdLogs` — node-level systemd unit logs: hostname, systemd unit, message

## Common Query Patterns

- All tables have `timestamp`, `environment`, `cluster`, `region` columns for scoping.
- Time filtering: `| where timestamp between (startTime .. endTime)`
- Service-specific tables add structured `log` (dynamic) with fields like `log.msg`, `log.content`.
- `frontendLogs` is the entry point for tracing a request (use `client_request_id` or `correlation_request_id`).
- `backendLogs` links to `frontendLogs` via `correlation_request_id` and contains state dumps from controllers.
- `containerLogs` is filtered by `namespace_name` and `container_name` to isolate maestro/hypershift logs.
- Maestro bundle tracking joins `containerLogs` + `backendLogs`/`clustersServiceLogs` via bundle ID extraction.

