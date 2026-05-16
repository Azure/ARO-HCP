# maestro / transitions

## Summary

Traces the full lifecycle of Maestro resource bundles through all seven transition layers (client to server spec,
broker, agent, and back), producing a per-bundle transition matrix.

## What to Look For

Roughly speaking, bundles should have:

- the same number of events in the "spec" side (`1_server_spec_from_client`, `2_server_spec_to_broker`,
  `3_agent_spec_from_broker`)
- `4_agent_acted_on_cluster` should be equal or larger than the "spec" event count
- roughly similar "status" side event counts, equal or larger than the "spec" event count (`5_agent_status_to_broker`, `6_server_status_from_broker`,
  `7_server_status_to_subscribers`)

| source          | label                                            | 1_server_spec_from_client | 2_server_spec_to_broker | 3_agent_spec_from_broker | 4_agent_acted_on_cluster | 5_agent_status_to_broker | 6_server_status_from_broker | 7_server_status_to_subscribers |
|-----------------|--------------------------------------------------|---------------------------|-------------------------|--------------------------|--------------------------|--------------------------|-----------------------------|--------------------------------|
| backend         | readonlyHypershiftHostedCluster (xxx)            | 2                         | 2                       | 2                        | 8                        | 40                       | 39                          | 121                            |
| clustersService | ManagedCluster /cid                              | 5                         | 5                       | 5                        | 16                       | 17                       | 16                          | 52                             |
| clustersService | ManifestWork local-cluster/cid                   | 13                        | 13                      | 13                       | 16                       | 27                       | 26                          | 81                             |
| clustersService | ManifestWork local-cluster/cid-00-hcp-namespaces | 2                         | 2                       | 2                        | 9                        | 5                        | 4                           | 15                             |
| clustersService | ManifestWork local-cluster/cid-00-namespaces     | 2                         | 2                       | 2                        | 7                        | 7                        | 6                           | 21                             |
| clustersService | ManifestWork local-cluster/cid-credential-id     | 2                         | 2                       | 2                        | 1                        | 5                        | 4                           | 15                             |
| clustersService | ManifestWork local-cluster/cid-csra-perm         | 2                         | 2                       | 2                        | 6                        | 6                        | 5                           | 18                             |
| clustersService | ManifestWork local-cluster/cid-np-1              | 5                         | 5                       | 5                        | 5                        | 12                       | 11                          | 36                             |
| clustersService | Secret ocm-arohcpci01-cid/breakglass-key-id      | 2                         | 2                       | 2                        | 5                        | 4                        | 3                           | 12                             |

## Where to Go Next

Determine the bundle ID experiencing layer transition issues using `discovery/clustersService/maestroBundleAssociations` and `discovery/backend/maestroBundleAssociations`, then review `logs/maestro/serverLogs` and `logs/maestro/agentLogs` for that bundle specifically.
