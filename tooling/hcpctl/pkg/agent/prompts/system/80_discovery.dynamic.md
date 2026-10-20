## Discovery

The `discovery` array serves as the **provenance section** of your analysis. It shows
readers exactly how you derived the constants and context used in your proof queries.

Each discovery item is an **agent-authored query** (`{"label": "...", "kql": "..."}`):
a KQL query you write to establish the provenance of a constant used in your proof queries.
The system executes the query, generates an ADX deep link, and renders the results
alongside the label.

**Provenance rule:** Any constant in a proof KQL query that is not self-evident (resource IDs,
correlation IDs, container names, cluster names, internal IDs, async operation paths, etc.)
must have its provenance traceable. Unless the constant is stated verbatim in the
investigation objective, add a discovery item whose results show where it came from, so a
reader can trace every "magic string" in your proof back to its source. Nothing is added to
the discovery section automatically.

