# clustersService / phases

## Summary

Traces lifecycle phase transitions for a cluster or node pool in Clusters Service, showing state changes over time.

## What to Look For

A cluster should transition from validating, to pending, installing, and ready. Then, during test cleanup, to
uninstalling:

| timestamp                | msg                                              |
|--------------------------|--------------------------------------------------|
| 2026-05-15T08:19:33.067Z | Cluster 'cid' created, now in 'validating' state |
| 2026-05-15T08:19:44.753Z | updating cluster 'cid' state to 'pending'        |
| 2026-05-15T08:21:09.598Z | updating cluster 'cid' state to 'installing'     |
| 2026-05-15T08:25:38.041Z | updating cluster 'cid' state to 'ready'          |
| 2026-05-15T08:40:32.633Z | updating cluster 'cid' state to 'uninstalling'   |

Node pools follow a similar pattern, and also transition to `uninstalling` during deletion:

| timestamp                | msg                                                                           |
|--------------------------|-------------------------------------------------------------------------------|
| 2026-05-15T08:28:47.214Z | Node pool 'np' created for cluster 'cid' with state 'validating'              |
| 2026-05-15T08:28:52.068Z | Node pool 'np' for cluster 'cid' state updated from 'pending' to 'installing' |
| 2026-05-15T08:37:43.07Z  | Node pool 'np' for cluster 'cid' state updated from 'installing' to 'ready'   |
| 2026-05-15T08:40:33.500Z | Node pool 'np' for cluster 'cid' state updated from 'ready' to 'uninstalling' |

## Where to Go Next

If the cluster or node pool:
- reaches `validating` but not `pending`, review `logs/clustersService/inflightChecks.md` to see which inflight check is stuck or failing.
- reaches `pending` but not `installing`, review `logs/clustersService/provisionSteps.md` to see which provision step is stuck or failing.
- reaches `installing` but not `ready`, review `logs/clustersService/logs.md` paying attention to timestamps, and review `conditions/hypershift/hostedClusterConditions.md` or `conditions/hypershift/nodePoolConditions.md` for the next layer of the stack.
- reaches `ready` in Clusters Service but the ARM create operation stays `Provisioning`, review `conditions/backend/resourceControllerConditions.md` and `conditions/hypershift/hostedClusterConditions.md`.

If the **cluster** reaches `uninstalling` but never completes, review `logs/clustersService/logs.md` for repeated `Not continuing to the next destructor` messages. If CS is still waiting for the HostedCluster to disappear, check both the backend's kube-applier removal chain and CS's `csReadonlyHypershiftHostedCluster` ReadDesire; stale mirrored content can keep CS waiting after deletion. Review `logs/kubeApplier/logs.md` and `conditions/backend/resourceControllerConditions.md`, and for clusters that still have a ManagedCluster, `conditions/acm/managedClusterConditions.md`, `events/acm/klusterletEvents.md`, and `logs/acm/klusterletLogs.md` for addon pre-delete pod evictions or scheduling failures.

If a **node pool** reaches `uninstalling` but never completes, review `logs/clustersService/logs.md` for repeated `destructor ... has not completed yet` messages. The node pool destruct chain has 4 steps: cluster-resources removal, NSG subnet dissociation, ReadDesire deletion, and DB cleanup. The NodePool CR itself is deleted by the backend through kube-applier; check `logs/kubeApplier/logs.md` and `conditions/hypershift/nodePoolConditions.md` for its state on the management cluster.
