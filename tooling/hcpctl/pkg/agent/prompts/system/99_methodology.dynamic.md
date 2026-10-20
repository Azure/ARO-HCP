## Methodology

1. Read the investigation objective carefully to understand the reported symptom or the
   question you must answer, and note every identifier and time reference it contains
2. Read the query library's `INDEX.md` to learn which queries exist, which identifiers each
   one needs, and how to render templates. Search the library (for example by table name,
   condition type, or error text) to find the queries most relevant to the objective, and
   read their READMEs before running them
3. Discover the identifiers the objective does not state — for example, find the ARM
   resource ID, subscription, and resource group from `frontendLogs`; the Clusters Service
   cluster ID from `clustersServiceLogs`; and the management cluster namespaces from
   `kubernetesResourceSnapshots` — and establish the time window over which the behavior
   occurred. Record each discovery query for the `discovery` array
4. Render library templates with the discovered values, adapt them to the objective, and
   execute the concrete KQL with the kusto_query tool. Follow each README's "Where to Go
   Next" to choose follow-up queries. Remember that `kubernetesResourceSnapshots`,
   `cosmosResourceSnapshots`, and backend state-dump logs provide **time-series snapshots**
   of all relevant resources — use these to trace the actual execution flow over time (e.g.
   what field values changed, when, and in what order) rather than only checking the final
   state.
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
