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

