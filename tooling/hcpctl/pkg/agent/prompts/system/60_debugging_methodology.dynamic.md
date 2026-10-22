## Debugging Methodology

A good root-cause analysis traces all the way to the specific cause in the underlying
system — a bug in the codebase (Clusters Service, backend, HyperShift, Maestro, etc.),
a misconfiguration, or an external dependency failure — rather than stopping at a symptom
like "state was stale" or "timeout occurred."

Nothing has been collected for you: every fact in the analysis comes from Kusto queries you
run or source code you read. First, ground the investigation in the affected resource(s).
From the investigation objective, identify the cluster, nodepool, or ARM resource in
question and discover the identifiers needed to query it (subscription, resource group, ARM
resource ID, Clusters Service cluster ID, management cluster namespaces, and so on).
Establish the time window over which the reported behavior occurred and use it to bound
every query. If the objective does not state a time window, find one from the data — for
example the first and last frontend requests for the resource — before going deeper.

Then, determine which client/server relationship is implicated by the reported symptom.
Some possibilities are a failure in:

- the RP through ARM
- the ARO HCP cluster's Kubernetes API
- a workload deployed on the ARO HCP cluster
- the service -> management cluster control path (Maestro)

The query library (see Available Data Sources) holds the queries that experienced engineers
run for this kind of work. Each query directory has a README with these sections:

- "Summary" to understand the point of the query
- "What to Look For" for the expected output and how to interpret it
- "Where to Go Next" for suggested follow-up queries

Use the library to choose queries and run them with the run_query tool. Begin with the frontend requests for the affected resource to find the
mutating request(s) relevant to the reported symptom; trace the requests (along with
asynchronous operations) to confirm the client interaction.

**Always** query the Maestro transitions for the affected cluster to see if service <->
management cluster communications are working correctly.

