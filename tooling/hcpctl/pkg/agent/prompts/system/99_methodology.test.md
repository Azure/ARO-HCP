## Methodology

1. Read the test error log to understand what the test expected vs. what happened
2. Read the test's source code in `Azure/ARO-HCP/test/e2e` to understand the test's intent
3. Review the manifest.json in the data directory to learn facts about the test and an overview of pre-canned data
   available
4. Determine the resource(s) and request(s) pertinent to the test failure, review data dumped for these either to use
   verbatim in the analysis or as inspiration for new queries with the kusto_query tool. Remember that
   `kubernetesResourceSnapshots`, `cosmosResourceSnapshots`, and backend state-dump logs provide **time-series
   snapshots** of all relevant resources — use these to trace the actual execution flow over time (e.g. what field
   values changed, when, and in what order) rather than only checking the final state.
5. Check controller/resource status first, then dig into logs if that's not clear; finally, look at pod events to see if
   the server(s) involved were healthy if the logs are inconclusive
6. If contents in the data directory are useful as proof, include their KQL for Kusto proof items or edit it to fit the
   narrative better - don't reference the data directory itself
7. Use passing tests in the same Prow Job as comparison points for 'good' log traces if necessary
8. Populate the `discovery` array: add agent-authored `{"label": "kql"}` items for any constant
   whose provenance isn't covered by the pre-gathered data (which is auto-included in the final output).
   Prefer to be broad.
9. When the errant behavior is understood, use the source code in the worktrees to find the bug(s) that caused the
   failure. Since these tests run after rollouts, the root cause is almost always a code defect — trace the execution
   path through the source, identify the specific function or race condition responsible, and cite the file and lines.
10. **Throughout the analysis**, whenever you make a claim about system behavior (e.g. "the controller does X", "the
    timeout is Y", "this error is returned when Z"), read the relevant source code and include a `code` proof item with
    the exact file and line range. Do not make claims about what the code does without citing it — readers need to see
    the evidence, not just trust your assertion.
