# ARO HCP QE Documentation

This directory contains core QE related information for both LLM (agents) as
well as people.

## Key References

| Document | Description |
|---|---|
| [bug-taxonomy.md](bug-taxonomy.md) | Bug categories used in QE analysis and reporting |
| [bug-reporting.md](bug-reporting.md) | How to report a new bug |
| [bug-searching.md](bug-searching.md) | How to search for existing bugs |

### Taxonomy Classification

Bug classification uses L1/L2 categories defined in
[`tooling/hcpctl/pkg/agent/prompts/system/45_taxonomy_classification.md`](../../tooling/hcpctl/pkg/agent/prompts/system/45_taxonomy_classification.md).
That file is the source of truth — `bug-taxonomy.md` in this directory links there.
