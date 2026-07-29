# alerts / service

## Summary

Lists fired or active Prometheus/metric alerts during the time window for ARO-HCP service namespaces (clusters-service, maestro, hypershift, fleet, etc.).

## What to Look For

Alerts indicating service-level degradation — pod restarts, resource exhaustion, failed reconciliations, or connectivity issues in service components that could cause test failures.

## Where to Go Next

Correlate with service component logs (e.g. `logs/clustersService/`, `logs/maestro/`) to determine whether the alert was a symptom or a root cause. Use the `kusto_query` tool to expand `alertContext` for full detail.
