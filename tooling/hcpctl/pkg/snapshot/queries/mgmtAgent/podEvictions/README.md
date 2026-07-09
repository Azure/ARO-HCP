# mgmtAgent / podEvictions

## Summary

Lists pod eviction events observed by the mgmt-agent PodWatcher for pods in the klusterlet namespace of this cluster. Filters for pods with `status.reason == "Evicted"`, which indicates the kubelet terminated the pod due to node resource pressure (e.g. MemoryPressure, DiskPressure).

## What to Look For

- Any results here indicate pods were evicted before they could complete. The `message` field shows the node condition that triggered the eviction (e.g. `"The node was low on resource: memory. Threshold quantity: ..."` or `"Pod was rejected: The node had condition: [MemoryPressure]."`).

- Addon pre-delete hook pods (`config-policy-controller-uninstall`, `governance-policy-framework-uninstall`) evicted during cluster deletion will block the ManagedCluster from completing Detaching, halting the entire CS destruct chain.

## Where to Go Next

If evictions are found, check if the destruct chain is stuck: review Clusters Service logs for repeated `hypershift-managed-cluster-destructor` iterations with `Not continuing to the next destructor`. The manual fix is to clear ManagedClusterAddon finalizers (see docs/ops/cleanup-stuck-cluster-deletion.md, Scenario 6).
