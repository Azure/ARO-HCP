### Taxonomy Classification

Every analysis MUST include a `classification` object that categorizes the
failure. This enables automated aggregation and triage by downstream systems.

#### L1 Categories

Assign exactly one L1 category based on where the failure originates:

- **Azure Problems** — ARM/RP throttling, quota exhaustion, regional outages,
  Azure platform errors, networking issues from Azure infrastructure
- **Deployment Failures** — configuration errors, secret issues, Helm/EV2
  deployment failures, image pull failures, rollout problems
- **Product Failures** — operator crashes, reconciliation errors, nil pointer
  dereferences, component bugs in ARO-HCP services or dependencies
- **Test Reliability** — cleanup/teardown failures, artifact upload issues,
  test infrastructure problems, flaky assertions, test-side timeouts unrelated
  to the product

#### L2 Subcategories

When `l1_category` is `"Product Failures"`, you MUST also set `l2_subcategory`
to identify the responsible component:

- **Frontend** — the ARM REST API endpoint (RP frontend)
- **Cluster Service** — Clusters Service (CS) cluster lifecycle management
- **Backend** — the RP backend for async operations
- **Maestro** — multi-cluster orchestration (server or agent)
- **HyperShift** — HyperShift operator, hosted control plane components, CAPI
- **RH Upstream** — bugs in upstream Red Hat components (MCE, ACM, OLM, etc.)

When `l1_category` is NOT `"Product Failures"`, omit `l2_subcategory`.

#### Confidence Score

Set `confidence` to a value between 0 and 1 indicating how certain you are
about the classification:

- **0.9–1.0** — strong evidence directly points to this category
- **0.7–0.9** — evidence is clear but some ambiguity remains
- **0.5–0.7** — classification is plausible but evidence is indirect
- **below 0.5** — low confidence; consider whether the analysis has enough
  evidence to support any classification

Base your confidence on the strength of the evidence in the causal chain, not
on the length of the chain itself.

