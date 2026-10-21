# Analysis Instructions

## The Recursive Why Method

Your analysis follows the "recursive why" method. Starting from the proximal
failure, you recursively ask "why?" to drill deeper into the root cause.

- The first question is always: **"Why did this test fail?"**
- The answer to each question must be a direct, specific response that fully
  describes one "layer" in our stack. For instance, explain fully the "why"
  as it pertains to the test runner, but don't jump into the frontend.
- **Make certain not to skip any "layers"** - for example, before talking
  about CAPI, explain the "why" for the frontend, backend, Clusters Service,
  and HyperShift.
- Each answer naturally raises a follow-up "why?" question that becomes the
  next link in the chain.
- The chain stops when you reach a root cause you can prove, or when you run
  out of evidence and must admit the trail ends.

## Standards For Proof

Every answer must be proven with verifiable evidence. Never speculate — always
prefer to admit that the answer to a "why?" is unclear or that more data is
necessary to understand the issue.

Keep track of what kinds of claims are being made in an answer, and ensure
**every single claim is proven**:

*Descriptive claims* (what did happen) are relatively easy to prove: provide
Kusto queries that clearly show the behavior in question.

*Normative claims* (what ought to happen) are difficult to prove, and should
be used with caution. A few common cases where they are permissible:

- if a server or client logs what they expected to see, what they did see
  and how the two did not match, both a normative and a descriptive claim
  can be proven with Kusto query output
- if the intent of the code is clear, a normative claim may be proven with
  an excerpt of code from the repo in question
- if neither of the above is possible, a normative claim may be supported
  with Kusto query output from passing sibling tests - by inference, the
  behavior seen in passing tests ought to happen

## Output Schema

Your final output MUST be a valid JSON object with this structure:

```json
{
    "root_cause": "Terse, one-sentence description of the root cause, as best we can tell",
    "summary": "Rich Markdown overview of the narrative that the chain links below will give full details over",
    "notes": "Free-form rich markdown content to expand on the summary",
    "chain": [
        {
            "question": "Why did this test fail?",
            "answer": "A direct, specific answer to the question",
            "notes": "Optional per-link commentary to flesh out the point",
            "proof": [
                {
                    "type": "log",
                    "source": "error",
                    "lines": [
                        1,
                        15
                    ],
                    "note": "The test error log shows the failure"
                },
                {
                    "type": "kusto",
                    "kql": "Self-contained KQL query",
                    "note": "Explain why this evidence supports the answer"
                },
                {
                    "type": "code",
                    "repo": "RepoName",
                    "file": "path/to/file.go",
                    "lines": [
                        10,
                        25
                    ],
                    "note": "Explain why this code is relevant to the answer"
                }
            ]
        },
        {
            "question": "Follow-up why question prompted by the previous answer",
            "answer": "...",
            "proof": [
                {
                    "type": "kusto",
                    "kql": "..."
                }
            ]
        }
    ],
    "suggestions": [
        "Actionable suggestion for improving debuggability, test resilience, adding logs, or preventing recurrence"
    ],
    "discovery": [
        {
            "label": "Derive the internal cluster ID from the correlation ID",
            "kql": "Self-contained KQL query"
        }
    ]
}
```

### Chain link rules

- The first chain link's `question` MUST be exactly `"Why did this test fail?"`
- The first chain link MUST include at least one `log` proof with `source` set
  to `"error"` — this anchors the analysis in the actual test failure output
- Each subsequent `question` must follow naturally from the previous `answer`
- Every `answer` must be directly supported by the `proof` items in that link
- Each `proof` item's optional `note` field should explain why that specific
  piece of evidence supports the answer — this is especially useful when a link
  has multiple proof items
- Every `proof` item is reproducible - `kusto` proofs can have their queries
  re-run, `code` proofs are rendered as links to hosted Git repositories from
  which the local worktrees are cloned, `log` proofs reference line ranges in
  the test error or output logs provided in the initial message
- For normative claims, use `code` proofs or comparative `kusto` proofs from
  passing sibling tests
- For descriptive claims, use `kusto` proofs
- **Proofs are mandatory for every single claim being made**

#### Output Schema Notes

- The `log` proof type is *only* for referring to content from the test's stdout
  or stderr logs, provided in the data directory under `test_logs/{output,error}.log`
- Use Markdown in all free-form text content to correctly format the output and
  improve communication efficacy.

## Markdown Formatting Rules

All free-form text fields (`root_cause`, `summary`, `notes`, `note`) are rendered as Markdown.
Use standard CommonMark syntax:

- **Lists:** Use `- item` or `1. item` syntax. Never use bullet characters like
  `•`, `–`, or other Unicode symbols — these are not recognized as list markup
  and will render as a single paragraph.
- **Code:** Use `` `inline` `` for identifiers and ` ``` ` fenced blocks for
  multi-line code or log excerpts. Always specify the language (e.g. `go`, `kql`,
  `json`) on fenced blocks.
- **Emphasis:** Use `**bold**` for key terms and `*italic*` for secondary emphasis.
- **Line breaks:** Use blank lines between paragraphs. Do not rely on trailing
  spaces or `\n` literals for line breaks within a JSON string — instead, use
  actual newlines in the JSON string value.

## Debugging Methodology

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

## Discovery

The `discovery` array serves as the **provenance section** of your analysis. It shows
readers exactly how you derived the constants and context used in your proof queries.

**All pre-gathered discovery data from the data directory is included automatically** in
the final output — every resource-level, request-level, and cluster-level discovery
directory is embedded deterministically. You do not need to reference any data directory
paths in your discovery array.

Each discovery item you provide is an **agent-authored query** (`{"label": "...", "kql": "..."}`):
a KQL query you write to establish the provenance of a constant used in your proof queries
that is NOT already covered by the pre-gathered data. The system executes the query, generates
an ADX deep link, and renders the results alongside the label.

**Provenance rule:** Any constant in a proof KQL query that is not self-evident (resource IDs,
correlation IDs, container names, cluster names, internal IDs, async operation paths, etc.)
must have its provenance traceable. If the constant is derived from pre-gathered discovery
data (which is auto-included), no explicit discovery item is needed. If the constant comes
from an ad-hoc investigation, add an agent-authored `{"label": "...", "kql": "..."}` item
so a reader can trace every "magic string" in your proof back to its source.

## KQL Quality Rules

**When a pre-canned query from the data directory is applicable to the analysis, prefer
to embed it directly (or add a few clauses to filter it before embedding).**

- Prefer the style exemplified in the gathered data under manifest.json:
    - always specify the full cluster, database, and table names unambiguously
    - wherever possible, make sure to add timestamp bounds that match the test's runtime
    - use new-lines liberally to keep the width of the query low
- Queries must be self-contained stories — a reader should understand the intent from the KQL alone
- Use `| summarize`, `| where`, `| project` to produce focused, unambiguous output
- To demonstrate absence, use `| summarize count = count()` — never rely on empty result sets
- Queries will be rendered verbatim alongside their results — write them as if presenting to a colleague

### Kusto Reference

Each regional Kusto cluster for ARO HCP has two databases:

- `ServiceLogs`, which contains data for the microservices making up the ARO HCP control plane, mostly composed of
  components running on the service cluster
- `HostedControlPlaneLogs`, which contains data for the individual customer clusters in the ARO HCP data plane, as well
  as logs for the management cluster components

Review the ingest mappings and schemas used to set up these Kusto databases and tables in the ARO-HCP repository, at
`dev-infrastructure/modules/logs/kusto/tables`.

Remember that hosted cluster namespaces will take the form `ocm-arohcp<env>-<cid>-<id>`. Use
`distinct pod_name, container_name` within a hosted cluster namespace to see the pods and containers for which we have
logs; or, search the `database('HostedControlPlaneLogs').table('containerLogs')` with
`| where namespace_name !contains 'ocm-arohcp'` to review other components on the management cluster for which we have
logs.

## Epistemological Rules

- Never assert a cause without proof from Kusto queries or source code
- Always dig in to the next causal layer - time-outs are a symptom, not a cause
- When pre-gathered data is insufficient, formulate ad-hoc Kusto queries to investigate further
- When you hit a dead end, state what you looked for and what you didn't find, with proof
- Do not speculate beyond what the evidence supports. The chain stops where the proof stops.
- Explore multiple hypotheses before committing to one — check alternatives

## Methodology

1. Read the test error log to understand what the test expected vs. what happened
2. Read the test's source code in `Azure/ARO-HCP/test/e2e` to understand the test's intent
3. Review the manifest.json in the data directory to learn facts about the test and an overview of pre-canned data
   available
4. Determine the resource(s) and request(s) pertinent to the test failure, review data dumped for these either to use
   verbatim in the analysis or as inspiration for new queries with the kusto_query tool
5. Check controller/resource status first, then dig into logs if that's not clear; finally, look at pod events to see if
   the server(s) involved were healthy if the logs are inconclusive
6. If contents in the data directory are useful as proof, include their KQL for Kusto proof items or edit it to fit the
   narrative better - don't reference the data directory itself
7. Use passing tests in the same Prow Job as comparison points for 'good' log traces if necessary
8. Populate the `discovery` array: add agent-authored `{"label": "kql"}` items for any constant
   whose provenance isn't covered by the pre-gathered data (which is auto-included in the final output).
   Prefer to be broad.
9. When the errant behavior is understood, use the source code in the worktrees to find the bug(s) that should be fixed
   to avoid this kind of error in the future
10. **Throughout the analysis**, whenever you make a claim about system behavior (e.g. "the controller does X", "the
    timeout is Y", "this error is returned when Z"), read the relevant source code and include a `code` proof item with
    the exact file and line range. Do not make claims about what the code does without citing it — readers need to see
    the evidence, not just trust your assertion.
