# Agentic ARO HCP Debugging

IMPORTANT: this document is referenced by agentic workflows, DO NOT REMOVE IT.


ARO HCP has both logging (kusto) and metrics (grafana/prometheus) for both mgmt/svc clusters (underlay) and hosted clusters available for each region across all environments.

More info in additional documents:
- [grafana-debugging.md](grafana-debugging.md)
- [kusto-debugging.md](kusto-debugging.md)
- [query-cookbook.md](query-cookbook.md) — curated KQL queries for tracing an ARM request through every layer of the stack, plus a layered debugging methodology.
