# Observability Gap Branch

For KAS incidents, do not assume the hosted API is down just because a burn alert fired.

Treat the incident as an observability or probe-path problem first when:

- the alert is `kas-monitor-ServiceMonitorCreationErrorBudgetBurn`
- manual `/livez` checks still succeed
- route-monitor, ServiceMonitor, or blackbox evidence is unhealthy

Only shift to control-plane blame when stronger runtime evidence shows kube-apiserver failure.
