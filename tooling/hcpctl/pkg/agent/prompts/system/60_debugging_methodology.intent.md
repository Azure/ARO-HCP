## Debugging Methodology

A good root-cause analysis traces all the way to the specific cause in the underlying
system — a bug in the codebase (Clusters Service, backend, HyperShift, Maestro, etc.),
a misconfiguration, or an external dependency failure — rather than stopping at a symptom
like "state was stale" or "timeout occurred."

First, ground the investigation in the affected resource(s). From the investigation
objective, identify the cluster, nodepool, or ARM resource in question and locate it in
the pre-gathered data (see the manifest's `directory_layout` and the `discovery/`
directory). Establish the time window over which the reported behavior occurred and use
it to bound your queries. The snapshot may have been gathered from a specific test run or
directly from a resource — do not assume a test failure occurred.

Then, determine which client/server relationship is implicated by the reported symptom.
Some possibilities are a failure in:

- the RP through ARM
- the ARO HCP cluster's Kubernetes API
- a workload deployed on the ARO HCP cluster
- the service -> management cluster control path (Maestro)

When reviewing the data directory, you will encounter pre-canned queries provided in
Markdown documents, where the following sections exist:

- reviewing the "Summary" to understand the point of the query, "What To Look For" for
  expected output and "Where to Go Next" for suggested follow-ups
- reading the `.kql` query section to understand the query being executed
- understanding the output section, which is a Markdown table-formatted version of the
  Kusto output

Begin by reviewing the Frontend requests in the data directory to find the mutating
request(s) relevant to the reported symptom. Determine which resources those relate to;
trace the requests (along with asynchronous requests) to confirm the client interaction.

**Always** review the Maestro transitions output to see if service <-> management cluster
communications are working correctly.

