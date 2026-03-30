# KAS / kube-apiserver Incident Investigator

## Scope

Primary ARO-HCP areas:

- `observability/alerts/HCPkasMonitor-prometheusRule.yaml`
- `observability/alerts/HCPkasRecord-prometheusRule.yaml`
- `observability/grafana-dashboards/kas-monitor/kas_monitor_api_slo.json`
- `route-monitor-operator/scaffold/kas-monitoring-hack.policy.yaml`
- `docs/monitoring.md`
- `docs/logging.md`
- `tooling/hcpctl/README.md`

## What this investigator cares about

- the 99.95 KAS availability promise
- whether `kas-monitor-*` means a real kube-apiserver outage or only a degraded probe path
- `/livez`, route-monitor, blackbox-exporter, and ServiceMonitor health
- whether hosted control plane evidence exists for the same time window

## Diagnostic checklist

- Did the incident start from `kas-monitor-ErrorBudgetBurn` or `kas-monitor-ServiceMonitorCreationErrorBudgetBurn`?
- Does `probe_success{probe_url=...}` show sustained failure or only intermittent burn?
- Do manual `/livez` checks succeed from the correct context?
- If `/livez` succeeds, is the route-monitor or blackbox path unhealthy instead?
- Is there matching hosted control plane evidence for `kube-apiserver` in the same window?

## High-value implementation hotspots

- `observability/alerts/HCPkasMonitor-prometheusRule.yaml`
- `observability/grafana-dashboards/kas-monitor/kas_monitor_api_slo.json`
- `route-monitor-operator/scaffold/kas-monitoring-hack.policy.yaml`
- `docs/monitoring.md`
- `docs/logging.md`
- `test/util/verifiers/kusto.go`

## High-signal investigation entry points

- `/home/swiencki/worktree/Azure-Documents-Common/Teams/Azure RedHat OpenShift/doc/hcp/troubleshooting/kube-api-availability-monitoring.md`
- `/home/swiencki/worktree/Azure-Documents-Common/Teams/Azure RedHat OpenShift/doc/hcp/troubleshooting/rmo-monitoring.md`

## Phase-1 fixture lesson

- `fixtures/historical-incidents/incident-002-kas-api-availability-burn.md` — separate probe-path degradation from real kube-apiserver unavailability before escalating into control-plane blame.
