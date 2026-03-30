# Fresh-Session Domain Flow

The kernel PR uses one main orchestrator plus a fresh-session child domain agent.

## Main orchestrator responsibilities

- normalize the incident envelope once
- route the incident to the matched domain
- dispatch the matched domain to its child agent
- synthesize the returned domain memo into one TSG draft

## Domain agent responsibilities

- receive the original incident input
- receive the normalized incident envelope unchanged
- read `sub-investigators/cross-cutting.md` plus its domain investigator
- return one structured domain memo, not a TSG

Even if only one domain matches, still use the child agent so the flow stays consistent.
