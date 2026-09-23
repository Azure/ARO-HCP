# alerts / uncategorized

## Summary

Lists fired or active Prometheus/metric alerts during the time window that do not belong to a known service namespace or a per-cluster namespace (hosted control plane, klusterlet).

**Be aware that alerts in here may only pertain to *other clusters* that happened to be running at this time. Proceed with caution.**

## What to Look For

Unexpected infrastructure alerts (node pressure, etcd issues, certificate expiry) that may indicate environmental instability affecting the test.

## Where to Go Next

Use the `kusto_query` tool to expand `alertContext` for any interesting alerts to see the full label set and annotations.
