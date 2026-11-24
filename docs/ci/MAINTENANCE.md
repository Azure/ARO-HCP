# CI Documentation Maintenance Guide

Notes for maintaining the CI documentation under `docs/ci/`.

## Principles

The CI docs explain how the system fits together and how to operate it. They are not a mirror of the code or of the `openshift/release` configuration, and they stay useful for longer when they avoid acting like one.

- **Explain each concept once.** Every concept has one home in the table below. Other pages give a sentence of context and a link. If you find yourself explaining MSI mocks, leasing or cleanup semantics a second time, link instead.
- **Slot-manager has its own design document.** The catalog, the asset lifecycle, admission, the state journal and the runtime contract are specified in `test/cmd/aro-hcp-tests/slot-manager/DESIGN.md`. CI pages link to its sections and never restate them.
- **Stay at the level a reader needs.** Describe what exists, why, who owns it and what to do. Leave out function and field names, long file lists and step-by-step internals that the code already shows.
- **Do not copy values that live elsewhere.** Slot and container counts, subscription names, quotas, regions, job names and schedules belong to the catalog, the config files and `openshift/release`. Point to them, and when a number is needed to make a point, say how to re-derive it.
- **Write for a person.** Use paragraphs that explain and connect, and use lists for real lists and ordered procedure steps. Avoid strings of clipped fragments, stacked caveats and commandments without a reason.
- **Verify before you write.** Check claims against the repository and, for release-side behavior, against `openshift/release`. If you cannot verify something, link to its source of truth instead of asserting it.

## Document Map

| Page | Home of | Update it when |
| :--- | :------ | :------------- |
| `README.md` | The reader journey through the set | pages are added, removed or renamed |
| `execution.md` | What each CI mode validates, the Azure request path per environment, run-cost tagging, configuration overrides | modes, tenant or subscription flow, or the slot acquisition model change |
| `image-lifecycle.md` | The CI image graph, promotion, the ACR push boundary | the build root, the job-local image graph or promotion rules change |
| `ev2-integration.md`, `ev2-retry-catcher.md` | EV2-to-Prow wiring, commit pinning, automatic retry | `prowJobName` or `gatePromotion` wiring, Gangway auth or retry eligibility change |
| `cleanup.md` | Why and how cleanup is split into three paths | cleanup semantics, ownership or strictness change |
| `slot-assets.md` | The three slot assets and how to provision and validate them | an asset is added, removed or changes lifecycle, or the asset commands change |
| `dev-mock-identities.md` | What each mock identity stands in for, its roles, certificates, slot mock preparation | a mock identity or its roles change, or certificate handling changes |
| `e2e-capacity.md` | Azure limits, how to check the catalog against them, how to change capacity | the formulas, the limits or the safe-change procedure change |
| `e2e-subscription-onboarding.md` | Onboarding ARO-HCP-managed subscriptions | the steps, the owning teams or the required grants change |
| `external-subscription-management.md` | External subscription reconciliation, onboarding and scheduled maintenance | the reconciliation command, supported environments, safety boundaries or required grants change |
| `dev-region-load-management.md` | Regional drain, rebalance and restore procedures | the way regions are selected or the procedure changes |
| `e2e-testing.md`, `inplace-upgrade-presubmit.md`, `hypershift-v2-presubmit.md`, `capz-e2e.md` | How to trigger and narrow E2E runs and what the specialised presubmits do | `/test` commands, suites or the presubmit flows change |
| `operations.md` | Inspecting runs, observability artifacts, changing CI configuration, troubleshooting | the way maintainers work with CI changes |
| `dev-ci-topology.md`, `opstool.md`, `dev-ci-monitoring.md`, `amw-platform-observability.md` | The standalone `dev-ci` rollout, its AKS platform, its alerts and the AMW report | service groups, ownership, alerts or the report change |
| `system-pool-only.md` | The single-node-pool mode for CI service clusters | the mode or its schema rules change |
| `periodic-analysis-instructions.md` | Instructions read by the scheduled digest agent | the periodic job set or the digest process changes; keep the path stable |

`operations.md` keeps the only job inventory, a small appendix. Do not reintroduce large per-job tables, schedule dumps or job-name lists in other pages, and do not recreate the old `prow.md` catalog.

## Sources Of Truth

Generated Prow job definitions (never edit by hand), under `ci-operator/jobs/Azure/ARO-HCP/` in `openshift/release`:

- `Azure-ARO-HCP-main-presubmits.yaml`, `-postsubmits.yaml` and `-periodics.yaml`

`openshift/release` source configuration (edit these), under `ci-operator/config/Azure/ARO-HCP/`:

- `Azure-ARO-HCP-main.yaml` and its `__baseimage-generator`, `__e2e`, `__periodic`, `__periodic-cleanup` and `__image-updater` variants

Step-registry workflows and steps live under `ci-operator/step-registry/aro-hcp/`.

Wiring in this repository:

- `test/e2e-config/e2e-slots.yaml`, the slot catalog, and `test/cmd/aro-hcp-tests/slot-manager/`
- `config/config.msft.clouds-overlay.yaml` and `test/e2e-pipeline.yaml`, for EV2 gating
- `config/config-dev-ci.yaml`, `topology-dev-ci.yaml` and `dev-infrastructure/dev-ci/`, for the `dev-ci` rollout and the mock identity RBAC
- `dev-infrastructure/openshift-ci/Dockerfile` and `test/Containerfile.e2e`, for the CI images

## Updating After A CI Change

When CI configuration changes in `openshift/release`, change the source configuration there, follow that repository's regeneration flow (`make update`), and touch these docs only if the change affects behavior or navigation. A job rename or a schedule tweak usually needs at most an update to the appendix in `operations.md`.

When a feature changes external-subscription prerequisites, update the shared configuration, Bicep templates or asset handlers consumed by `reconcile-external-subscription` in the same change. A new manual runbook step is not a substitute for command support. Document deliberate identity and capacity maintenance separately.

## Before Finalising A Doc Change

- Every link and anchor resolves, and headings stay unique within a page.
- The concept you touched is explained only in its home page.
- No catalog values, job lists or quota figures were copied into prose.
- Procedures still match the Make targets and commands they cite.
