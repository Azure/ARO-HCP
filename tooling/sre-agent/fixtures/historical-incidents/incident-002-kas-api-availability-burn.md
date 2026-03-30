# Incident 002: KAS API availability burn without a confirmed control-plane outage

## Why it mattered

This fixture proves the kernel KAS investigation pattern end to end: start from the customer-facing availability signal, then decide whether the incident shows a real kube-apiserver outage or only a degraded probe / monitoring path.

## Scenario

- Customer symptom: kubeconfig access is reported as intermittent or unavailable.
- Initial evidence: `kas-monitor-ErrorBudgetBurn` or `kas-monitor-ServiceMonitorCreationErrorBudgetBurn`, optionally with a `probe_url` and affected HCP namespace.
- First-pass question: is the hosted API unhealthy, or is the route-monitor / blackbox / ServiceMonitor path unhealthy?

## High-signal investigation steps

1. Graph `probe_success{probe_url=...}` over the alert window and capture whether the failures are sustained or intermittent.
2. Determine whether the endpoint is public or private, then check `/livez` from the correct context.
3. If `/livez` succeeds, inspect route-monitor, blackbox-exporter, and ServiceMonitor coverage before blaming kube-apiserver.
4. If `/livez` fails or hosted control plane evidence already exists, inspect `kube-apiserver` logs and events in the same window.

## Reusable lesson

`probe_success` is useful only when the probe path itself is healthy. Separate real kube-apiserver unavailability from monitoring-path failures before escalating into control-plane blame.
