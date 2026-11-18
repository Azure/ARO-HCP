# Bug Taxonomy

The bug taxonomy classification used across all QE analysis and reporting is
defined in:

[`tooling/hcpctl/pkg/agent/prompts/system/45_taxonomy_classification.md`](../../tooling/hcpctl/pkg/agent/prompts/system/45_taxonomy_classification.md)

That file is the single source of truth for:

- L1 categories (Azure Problems, Deployment Failures, Product Failures, Test Reliability)
- L2 subcategories for Product Failures (Frontend, Cluster Service, Backend, Maestro, Kube Applier, HyperShift, …)
- Classification rules and the required `classification` object schema
