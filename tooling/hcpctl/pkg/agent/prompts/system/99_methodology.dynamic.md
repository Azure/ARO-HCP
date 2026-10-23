## Methodology

1. Read the investigation objective carefully to understand the reported symptom or the
   question you must answer, and note every identifier and time reference it contains
2. Use list_queries to see which queries exist and what each needs, and describe_query to
   read the READMEs of the queries most relevant to the objective. The library overview in
   the initial message explains which identifiers other queries depend on
3. Establish the time window over which the behavior occurred and the identifiers the
   objective does not state. Prefer the library's discovery queries — for example
   `frontend/frontendRequests` to find the ARM resource ID, `clustersService/cid` for the
   Clusters Service cluster ID, and `hypershift/hostedClusterMetadata` for the management
   cluster namespaces — and write ad-hoc kusto_query queries when they do not apply. Record
   each discovery query for the `discovery` array
4. Run library queries with run_query, passing new identifiers and time windows as you learn
   them, and follow each README's "Where to Go Next" to choose follow-up queries. Adapt a
   query to the objective by running its rendered KQL, edited, with the kusto_query tool.
   Remember that `kubernetesResourceSnapshots`, `cosmosResourceSnapshots`, and backend
   state-dump logs provide **time-series snapshots** of all relevant resources — use these to
   trace the actual execution flow over time (e.g. what field values changed, when, and in
   what order) rather than only checking the final state.
5. Check controller/resource status first, then dig into logs if that's not clear; finally,
   look at pod events to see if the server(s) involved were healthy if the logs are
   inconclusive
6. Use the rendered library queries that support your findings as Kusto proof items, editing
   them to fit the narrative better
7. When a baseline is needed to support a normative claim, compare against a known-good
   cluster or a comparable time window on the same cluster
8. Populate the `discovery` array: add agent-authored `{"label": "kql"}` items for every
   constant used in your proofs that is not stated verbatim in the objective. Prefer to be
   broad.
9. When the errant behavior is understood, use the source code in the worktrees to trace the
   execution path through the system, identify the specific function, race condition, or
   configuration responsible, and cite the file and lines. The root cause may be a code
   defect, a misconfiguration, or an external dependency failure — follow the evidence.
10. **Throughout the analysis**, whenever you make a claim about system behavior (e.g. "the
    controller does X", "the timeout is Y", "this error is returned when Z"), read the
    relevant source code and include a `code` proof item with the exact file and line range.
    Do not make claims about what the code does without citing it — readers need to see the
    evidence, not just trust your assertion.
