## Methodology

1. Read the investigation objective carefully to understand the reported symptom and the
   question you must answer
2. Review the manifest.json in the data directory to learn facts about the affected
   resource(s) and an overview of the pre-canned data available. If diagnostic logs
   (`test_logs/`) or sibling snapshots are present, use them as additional context, but do
   not assume the objective is a test failure
3. Identify the resource(s) and request(s) pertinent to the reported symptom, and establish
   the time window over which it occurred. Review the data dumped for these either to use
   verbatim in the analysis or as inspiration for new queries with the kusto_query tool.
   Remember that `kubernetesResourceSnapshots`, `cosmosResourceSnapshots`, and backend
   state-dump logs provide **time-series snapshots** of all relevant resources — use these
   to trace the actual execution flow over time (e.g. what field values changed, when, and
   in what order) rather than only checking the final state.
4. Check controller/resource status first, then dig into logs if that's not clear; finally,
   look at pod events to see if the server(s) involved were healthy if the logs are
   inconclusive
5. If contents in the data directory are useful as proof, include their KQL for Kusto proof
   items or edit it to fit the narrative better - don't reference the data directory itself
6. When a baseline is needed to support a normative claim, compare against a known-good
   cluster or a comparable time window on the same cluster
7. Populate the `discovery` array: add agent-authored `{"label": "kql"}` items for any
   constant whose provenance isn't covered by the pre-gathered data (which is auto-included
   in the final output). Prefer to be broad.
8. When the errant behavior is understood, use the source code in the worktrees to trace the
   execution path through the system, identify the specific function, race condition, or
   configuration responsible, and cite the file and lines. The root cause may be a code
   defect, a misconfiguration, or an external dependency failure — follow the evidence.
9. **Throughout the analysis**, whenever you make a claim about system behavior (e.g. "the
   controller does X", "the timeout is Y", "this error is returned when Z"), read the
   relevant source code and include a `code` proof item with the exact file and line range.
   Do not make claims about what the code does without citing it — readers need to see the
   evidence, not just trust your assertion.
