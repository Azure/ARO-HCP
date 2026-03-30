---
name: arohcp-sre-agent
description: Investigate ARO-HCP kube-apiserver availability incidents and return a TSG-shaped analysis grounded in the local repo.
compatibility:
  tools:
    - view
    - rg
    - glob
    - bash
disable-model-invocation: true
context: fork
agent: arohcp-sre-agent
---

# ARO-HCP SRE Agent

Launch the in-repo ARO-HCP SRE kernel investigator in a fresh context so it can reason about kube-apiserver availability incidents without reusing the caller's working memory.

Use the current user request as the incident input.

Return only the forked runtime agent's TSG draft. Do not add preambles, summaries, or wrapper text before the `# TSG:` title.

Incident input: `$ARGUMENTS`
