# Node egress public IP observation

`ObserveEgressPublicIPs` owns
`ServiceProviderCluster.Status.AzureResources.EgressPublicIPs`. The map is keyed
by full ARM resource ID and records `Found`, `NotFound`, or `Unknown`. An absent
entry is unobserved, not evidence of absence. The controller replaces only this
field and its entry in `Spec.EarliestRecheckTimesByController`, using the
service-provider document's optimistic concurrency token.

For LoadBalancer outbound clusters (including the legacy empty outbound value),
the expected resources are nine `Microsoft.Network/publicIPAddresses` in the
customer subscription's managed resource group, named `<cluster-service-ID>-0`
through `<cluster-service-ID>-8`. These are per cluster, not per node pool.
The ID is the terminal `ClusterServiceID.ID()` value, also used by the backend's
TLS certificate naming. Other public IPs cannot satisfy the check.

The controller resolves the customer tenant from the subscription and performs
only ARM GETs through the first-party application client. HTTP 404 with
`ResourceNotFound` or `ResourceGroupNotFound` means absent; other responses leave
the affected resources unknown and retry normally.
Before retrying an observation, previous missing observations are durably marked
unknown. If invalidation conflicts, no Azure read is attempted. If the final
observation write conflicts or fails, the durable unknown state prevents an older
missing-resource diagnostic from representing a failed read. Missing cluster,
service-provider, subscription, tenant, cluster-service ID, or resource-group
name prerequisites wait for later reconciliation. UDR and deletion are skipped.
Existence is sufficient; provisioning state and address assignment are not gates.
Once all expected resources are confirmed, observation stops. Pending resources
are rechecked on the controller's five-minute resync or relevant cache events.
A pass with missing resources, including a pass with other reads failing, sets a
jittered one-to-one-and-a-half minute minimum recheck time and explicitly queues
that retry. Early status events reschedule the remaining delay without reads or
document replacements, preventing self-triggered invalidation/read cycles.
Read failures remain unknown and are returned as errors; passes without missing
resources retain normal error retries.

Cluster creation includes an `egressPublicIPs` completion source. Missing or
unknown resources keep the operation provisioning rather than returning a Go
error. The existing creation deadline therefore includes the remaining check in
its diagnostic. A missing-resource message suggests checking Azure Policy without
asserting policy denial; unknown/read-failure states use a separate safe message.
Raw Azure errors are not copied to customer operation errors. Existing stronger
terminal errors and error-code aggregation retain precedence.

This controller creates, updates, and deletes no Azure resources, does not change
Cluster Service behavior, and does not stop downstream retries.
