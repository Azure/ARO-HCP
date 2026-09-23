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

