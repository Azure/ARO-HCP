# alerts / acm

## Summary

Lists fired or active Prometheus/metric alerts during the time window for the ACM klusterlet namespace (`klusterlet-<cluster-id>`) associated with the cluster under test.

ACM was used to inject ingress wiring for older clusters, so it's unlikely to be important - but failures here can block deletion of clusters that still have a ManagedCluster.

## What to Look For

Registration failures, agent connectivity issues, or addon errors that would block ManagedCluster detaching during cluster deletion.

## Where to Go Next

Review `logs/kubeApplier/logs.md` for the ManagedCluster removal step and `clustersService/logs/` for any errors. Use the `kusto_query` tool to expand `alertContext` for full alert detail.
