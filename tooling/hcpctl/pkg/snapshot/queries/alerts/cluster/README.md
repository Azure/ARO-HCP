# alerts / cluster

## Summary

Lists fired or active Prometheus/metric alerts during the time window for the hosted control plane namespace of the cluster under test.

## What to Look For

Control plane alerts — etcd leader loss, kube-apiserver latency, OOM kills, certificate rotation failures — that directly impact cluster health and may explain test timeouts or failures.

## Where to Go Next

Cross-reference with `events/hypershift/controlPlaneEvents` and `conditions/hypershift/hostedClusterConditions` to build a timeline. Use the `kusto_query` tool to expand `alertContext` for full alert detail.
