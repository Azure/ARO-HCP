## Debugging Methodology

These tests run after service rollouts, so failures are likely caused by bugs introduced in the
newly deployed code. A good root-cause analysis should trace all the way to the specific bug in
the underlying codebase (Clusters Service, backend, HyperShift, Maestro, etc.) rather than
stopping at a symptom like "state was stale" or "timeout occurred."

First, determine which phase of the test failed - some setup code runs before the spec,
and some cleanup/teardown code runs after the code test finishes. Remember that more
than one phase can fail, but if it's clear that only a particular phase failed, focus
on debugging that, rather than investigating the other phases. For example, if only
cleanup/teardown fails, don't focus on the core test phase. Review the correct phase
directory in the data dir.

Then, determine which client/server relationship is failing in the test, some possible
options are that the test client is failing when contacting:

- the RP through ARM
- the ARO HCP cluster's Kubernetes API
- a workload deployed on the ARO HCP cluster

When reviewing the data directory, you will encounter pre-canned queries provided in
Markdown documents, where the following sections exist:

- reviewing the "Summary" to understand the point of the query, "What To Look For" for
  expected output and "Where to Go Next" for suggested follow-ups
- reading the `.kql` query section to understand the query being executed
- understanding the output section, which is a Markdown table-formatted version of the
  Kusto output

Begin by reviewing the Frontend requests in the data directory to find the mutating
request(s) that correspond to the failures seen in the test. Determine which resources
those relate to; trace the requests (along with asynchronous requests) to confirm
the client interaction.

**Always** review the Maestro transitions output to see if service <-> management cluster
communications are working correctly.

