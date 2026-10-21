# alerts / acm

## Summary

Lists fired or active Prometheus/metric alerts during the time window for the ACM klusterlet namespace (`klusterlet-<cluster-id>`) associated with the cluster under test.

ACM is used by clusters-service to inject ingress and manage a few things, so it's unlikely to be important - but failures here can block deletions.

## What to Look For

Registration failures, agent connectivity issues, or work-agent errors that would prevent ManifestWork delivery to the hosted cluster — blocking cluster ingress provisioning or deletion.

## Where to Go Next

Review `clustersService/logs/` for any errors. Use the `kusto_query` tool to expand `alertContext` for full alert detail.
