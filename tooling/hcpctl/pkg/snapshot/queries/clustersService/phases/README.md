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

Node pools do something similar, without an uninstalling phase:

| timestamp                | msg                                                                           |
|--------------------------|-------------------------------------------------------------------------------|
| 2026-05-15T08:28:47.214Z | Node pool 'np' created for cluster 'cid' with state 'validating'              |
| 2026-05-15T08:28:52.068Z | Node pool 'np' for cluster 'cid' state updated from 'pending' to 'installing' |
| 2026-05-15T08:37:43.07Z  | Node pool 'np' for cluster 'cid' state updated from 'installing' to 'ready'   |

## Where to Go Next

If the cluster or node pool:
- reaches `validating` but not `pending`, review `logs/clustersService/inflightChecks.md` to see which inflight check is stuck or failing.
- reaches `pending` but not `installing`, review `logs/clustersService/provisionSteps.md` to see which provision step is stuck or failing.
- reaches `installing` but not `ready`, review `logs/clustersService/logs.md` paying attention to timestamps, and review `conditions/hypershift/hostedClusterConditions.md` or `conditions/hypershift/nodePoolConditions.md` for the next layer of the stack.
- reaches `ready` in Clusters Service but the ARM create operation stays `Provisioning`, review `conditions/backend/resourceControllerConditions.md` and `conditions/hypershift/hostedClusterConditions.md`.
- reaches `uninstalling` but never completes, review `clustersService/logs` for repeated `Not continuing to the next destructor` messages, and check `mgmtAgent/podEvictions` for addon pre-delete pod evictions that may be blocking the destruct chain.
