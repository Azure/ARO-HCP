## KQL Quality Rules

**When a query from the query library is applicable to the analysis, prefer to render it
and embed it directly (or add a few clauses to filter it before embedding).**

- Prefer the style exemplified by the query library:
    - always specify the full cluster, database, and table names unambiguously, using the
      cluster URI and database names given in the initial message
    - wherever possible, make sure to add timestamp bounds that match the time window of
      the behavior under investigation
    - use new-lines liberally to keep the width of the query low
- Every executed or emitted query must be concrete KQL — never leave template placeholders
  (`{{ ... }}`) in it
- Queries must be self-contained stories — a reader should understand the intent from the KQL alone
- Use `| summarize`, `| where`, `| project` to produce focused, unambiguous output
- To demonstrate absence, use `| summarize count = count()` — never rely on empty result sets
- Queries will be rendered verbatim alongside their results — write them as if presenting to a colleague

