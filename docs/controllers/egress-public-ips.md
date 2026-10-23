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
one logical resource-group-scoped public IP enumeration through the first-party
application client. The SDK List pager issues a read-only HTTP GET for each page
and follows every `nextLink`; enumeration is not necessarily a single request,
and SDK retries may issue additional requests.
No per-IP GETs or name-prefix filters are used. Full ARM IDs are matched locally,
case-insensitively, so duplicates and unrelated resources cannot satisfy the gate.
Only a fully successful enumeration with valid records establishes absence.
Any page error, including HTTP 404, leaves unconfirmed IDs unknown. Nil records
or missing/invalid ARM IDs also prevent deriving absence. IDs confirmed on valid
pages may remain found even when a later page fails.
Before retrying an observation, previous missing observations are durably marked
unknown. If invalidation conflicts, no Azure read is attempted. If the final
observation write conflicts or fails, the durable unknown state prevents an older
missing-resource diagnostic from representing a failed read. Missing cluster,
service-provider, subscription, tenant, cluster-service ID, or resource-group
name prerequisites wait for later reconciliation. UDR and deletion are skipped.
Existence is sufficient; provisioning state and address assignment are not gates.
Once all expected resources are confirmed, observation stops. Pending resources
are rechecked on the controller's five-minute resync or relevant cache events.
A pass with missing resources or an observation error sets a jittered
one-to-one-and-a-half minute minimum recheck time and explicitly queues that
retry. Early status events reschedule the remaining delay without enumeration
or service-provider document replacements, preventing self-triggered
invalidation/read cycles even after a partial enumeration. Observation errors
are still returned; unconfirmed IDs remain unknown rather than missing.

Cluster creation includes an `egressPublicIPs` completion source. Missing or
unknown resources keep the operation provisioning rather than returning a Go
error. The existing creation deadline therefore includes the remaining check in
its diagnostic. A missing-resource message suggests checking Azure Policy without
asserting policy denial; unknown/read-failure states use a separate safe message.
Raw Azure errors are not copied to customer operation errors. Existing stronger
terminal errors and error-code aggregation retain precedence.

This controller creates, updates, and deletes no Azure resources, does not change
Cluster Service behavior, and does not stop downstream retries.
