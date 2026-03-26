# Seed Historical Lessons

These are the initial reusable lessons mined from representative recent merged PRs. They are seeds, not the full corpus.
Each section records the canonical fixture and the seeded routing domains so the lesson is easy to trace back to the authoritative corpus.

## PR #4536 — nodepool version validation tightening

- **Fixture:** `fixtures/historical-prs/pr-4536-nodepool-version-validation.md`
- **Domains:** `resource-provider-api`

- Reviewers pushed for exact semver parsing rather than helper logic that accepted `X.Y` when product behavior required `X.Y.Z`.
- Reviewers cared about update semantics: stricter validation for new writes must not unnecessarily block unrelated updates to older persisted objects.
- Good review comments asked for concrete error messages and specific tests for allowed and rejected version shapes.

## PR #4318 — removing generic `context deadline exceeded` retries

- **Fixture:** `fixtures/historical-prs/pr-4318-pipeline-retry-scope.md`
- **Domains:** `cluster-service`, `maestro`, `lifecycle-operators`, `config-pipelines`

- A tiny pipeline diff can still be high blast radius when it touches many deployment units.
- Human rationale was simple but important: retrying a generic timeout string was too broad and risked masking real failures.

## PR #4555 — Shoebox log forwarding / observability rollout

- **Fixture:** `fixtures/historical-prs/pr-4555-shoebox-log-forwarding.md`
- **Domains:** `azure-infra-bicep`, `config-pipelines`, `observability-testing-tooling`

- Observability and infra changes were accepted partly because the PR carried fixture updates and explicit rollout confidence from prior E2E runs.
- The PR also documented a related follow-up dependency (`#4385`) rather than pretending the diff was self-contained.
- This is a good example of mgmt-only gating being part of review correctness.

## PR #4557 — ImageDigestMirrors plumbing

- **Fixture:** `fixtures/historical-prs/pr-4557-image-digest-mirrors.md`
- **Domains:** `resource-provider-api`, `observability-testing-tooling`

- API/model plumbing touched TypeSpec, OpenAPI, internal API, deepcopy, validation, OCM conversion, test SDK, E2E setup, and verifiers together.
- The review context distinguished a unit flake from a product regression and explicitly pushed the flake chase into follow-up work.

## PR #1766 — billing documents in Cosmos DB

- **Fixture:** `fixtures/historical-prs/pr-1766-billing-documents-cosmos.md`
- **Domains:** `backend-state`

- Reviewers pushed for explicit identity semantics in persisted billing documents instead of vague composite naming that hid what downstream systems actually keyed on.
- High-signal comments rejected clever error handling that assumed current failure modes and instead asked for direct error propagation plus explicit query-cardinality handling.
- Reviewers cared about lifecycle correctness too: duplicate matches and missing deletion timestamps could turn into silent over-billing or stale state.

## PR #2252 — cluster-service SLO dashboards and alerting rules

- **Fixture:** `fixtures/historical-prs/pr-2252-cluster-service-slo-alerting.md`
- **Domains:** `cluster-service`, `observability-testing-tooling`

- Reviewers cross-checked SLO prose, PromQL, numerical thresholds, generated rule outputs, and tested-rule fixtures rather than trusting any one layer in isolation.
- A strong review caught that `99.9%` and `99%` were being mixed across dashboards and rules, and that the human description mentioned timeout behavior that the query did not encode.
- Operational review also enforced alert-budget expectations, including keeping the rule set within the sev-3 policy envelope.

## PR #1229 — maestro agent metrics

- **Fixture:** `fixtures/historical-prs/pr-1229-maestro-agent-metrics.md`
- **Domains:** `maestro`

- Reviewers preferred the simplest deployable shape: use upstream/MCR images directly when mirroring adds no control value.
- Good comments optimized for runtime visibility by sending errors to `stderr` and removing file-based logging/volume plumbing that no longer served a purpose.
- The review signal was not “add metrics”; it was “add metrics without increasing operational surface area unnecessarily.”

## PR #1073 — use helm for PKO

- **Fixture:** `fixtures/historical-prs/pr-1073-use-helm-for-pko.md`
- **Domains:** `lifecycle-operators`

- Reviewers treated a Helm/operator migration as a runtime-security and ownership change, not just a packaging refactor.
- Good review comments questioned whether a custom image was necessary, whether obsolete build glue still remained, and whether MI names, namespaces, and service accounts belonged in `config.yaml`.
- Reviewers also pushed on least-privilege RBAC because the service account used during bootstrap/runtime would retain whatever broad role the chart assigned.

## PR #3954 — control plane upgrade controller flow

- **Fixture:** `fixtures/historical-prs/pr-3954-control-plane-upgrade-controller-flow.md`
- **Domains:** `resource-provider-api`, `backend-state`, `observability-testing-tooling`

- Reviewers pushed hard on upgrade-path semantics: decision logic had to consider the actual version, next-minor availability, and the real upgrade graph rather than a simplified shortcut.
- Good review comments rejected duplicated state when the needed data already existed in customer or persisted objects.
- The review signal also rewarded explicit decomposition of resolver logic because multi-step upgrade controllers become unreadable quickly when path selection, desired-version resolution, and state reads are mixed together.
- Later historical review of the merged flow reinforced a lifecycle lesson: temporary `active_versions` and `desired_version` state must be pruned or cleared once the reconcile outcome is reached, or stale state can bias future upgrade picks and retrigger obsolete work.
- Another strong lesson was to distinguish "next minor channel missing" from "queried version missing in the next-minor graph." Those are different operational states and can lead to different safe fallback choices.

## PR #3679 — admin API breakglass sessions

- **Fixture:** `fixtures/historical-prs/pr-3679-breakglass-sessions.md`
- **Domains:** `resource-provider-api`, `lifecycle-operators`, `config-pipelines`

- Reviewers treated this as a security- and rollout-sensitive admin surface, not just an API handler addition.
- High-signal comments pushed for immutable/required schema fields, deriving cluster identity from the HCP resource ID instead of user input, and better field documentation so operators knew what values actually meant.
- Because the change touched policy, config, rendered outputs, deployment templates, and docs together, reviewers implicitly held it to a cross-surface consistency bar.

## PR #1633 — deploy self managed prometheus

- **Fixture:** `fixtures/historical-prs/pr-1633-self-managed-prometheus.md`
- **Domains:** `backend-state`, `cluster-service`, `maestro`, `lifecycle-operators`, `azure-infra-bicep`, `config-pipelines`, `observability-testing-tooling`

- Reviewers treated a repo-wide observability rollout as an operational design change, not a pile of service monitors.
- Good comments asked how the new Prometheus instance would be monitored, whether persistence and anti-affinity choices were resilient enough, and whether the selected monitors actually made sense for the environment.
- Review context also cared about docs and external references being aligned with the concrete remote-write/workload-identity implementation instead of leaving the rollout as tribal knowledge.

For concrete path sets and rationale, read `fixtures/historical-prs/`.
