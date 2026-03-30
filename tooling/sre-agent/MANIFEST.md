# SRE Agent Manifest

This file is the index for the ARO-HCP SRE kernel PR. Treat the listed files as the authoritative kernel slice.

The kernel PR is intentionally small:

- Copilot-only entrypoint
- one main orchestrator
- fresh-session child-agent flow
- always-load cross-cutting guidance
- one strong domain: `kube-apiserver`
- one proof fixture
- minimal validation and smoke flow

## Entry points

- `SKILL.md`
- `agents/arohcp-sre-agent.md`
- `agents/arohcp-sre-kube-apiserver.md`
- `AGENTS.md`
- `Makefile`
- `common/tools/validate_sre_assets.py`
- `common/tools/smoke_sre_agent.py`

## Domain investigators

- `sub-investigators/cross-cutting.md`
- `sub-investigators/kube-apiserver.md`

## Shared assets

- `common/symptom-routing/routing.json`
- `common/output-contract/tsg-format.md`
- `common/output-contract/domain-memo-format.md`
- `common/investigation/incident-envelope.md`
- `common/investigation/evidence-ladder.md`
- `common/investigation/observability-gap-branch.md`
- `common/investigation/fresh-session-domain-flow.md`
- `common/security-privacy/redaction-rules.md`
- `common/self-check/final-pass.md`
- `common/scope-boundaries/non-goals.md`

## Proof fixture

- `fixtures/historical-incidents/incident-002-kas-api-availability-burn.md`

## Evaluation

- `tests/README.md`

## Out of scope for this PR

- RP/API routing and domain assets
- incident-bundle ingestion
- repo freshness helpers and mirror snapshots
- payload-aware OpenShift version resolution
- query playbooks and broader hardening
