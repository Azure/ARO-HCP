# Design: Auto-Routing Classified CI Failures to Component Teams

**Spike ticket:** [ARO-28146](https://redhat.atlassian.net/browse/ARO-28146)  
**Status:** DRAFT — ready for initial review  
**Author:** Giulio Frasca

## Problem Statement

The CI Failure Taxonomy (ARO-28136) classifies every LLM-analyzed failure with an L1 category and, for Product Failures, an L2 subcategory identifying the responsible component. Today this classification is produced by `hcpctl snapshot analyze` but nothing consumes it for routing — triage remains fully manual via Slack and Jira.

This spike answers two questions:
1. **Where does routing run?**
2. **How does L2 map to a team?**

## L2 Subcategory → Team Mapping

The taxonomy defines six L2 subcategories under "Product Failures." The mapping to teams is derived from the repo's OWNERS files:

> **Reviewers:** Please suggest triage owners for the "Team / Triage Owner" column below. OWNERS approvers are listed for reference but are architects/senior engineers, not necessarily the day-to-day triage contacts.

| L2 Subcategory  | Repo Directory             | OWNERS Approvers                          | Team / Triage Owner | Jira Component (proposed) |
|-----------------|----------------------------|-------------------------------------------|---------------------|---------------------------|
| Frontend        | `frontend/`                | mbarnes, deads2k, geoberle                | TBD                 | `aro-hcp-frontend`        |
| Backend         | `backend/`                 | mbarnes, geoberle, janboll, miguelsorianod| TBD                 | `aro-hcp-backend`         |
| Cluster Service | `cluster-service/`         | geoberle, janboll, miguelsorianod         | TBD                 | `aro-hcp-cluster-service` |
| Maestro         | `maestro/`                 | geoberle, stevekuznetsov, janboll         | TBD                 | `aro-hcp-maestro`         |
| HyperShift      | `hypershiftoperator/`      | geoberle, janboll, stevekuznetsov         | TBD                 | `aro-hcp-hypershift`      |
| RH Upstream     | (multiple upstream repos)  | (varies by upstream project)              | TBD                 | `aro-hcp-upstream`        |

### Notes on "RH Upstream"

"RH Upstream" covers failures in upstream OpenShift components that don't map to any of the other five L2 subcategories (e.g., `openshift/cluster-authentication-operator`, `openshift/machine-config-operator`, RHCOS). There is no single team to route to — upstream ownership is external to this repo. Options:

- **Route to the SRE team** (`sre-approvers` alias) for escalation, since they already handle cross-team operational issues
- **Leave unrouted** with a `needs-upstream-triage` label for manual handling during weekly triage
- **Build a static upstream mapping** from common upstream components to their known owners (e.g., `cluster-authentication-operator` → Team X) and expand it over time as patterns emerge

**Recommendation:** Start with `needs-upstream-triage` label only (no auto-assignment). Once enough upstream failures accumulate to show patterns, build the static mapping for the most frequent offenders. Avoids burdening any single team with a triage inbox they didn't sign up for.

### Mapping Maintenance

The mapping must be configurable without code changes. Options for where it lives:

| Location                                                       | Pros                                                           | Cons                                     |
|----------------------------------------------------------------|----------------------------------------------------------------|------------------------------------------|
| **YAML config in repo** (e.g., `config/taxonomy-routing.yaml`) | Version-controlled, PR-reviewed, co-located with taxonomy code | Requires deploy cycle to update          |
| **Jira automation rule config**                                | Updated in Jira UI, no deploy needed                           | Not version-controlled, harder to audit  |
| **ConfigMap on opstool cluster**                               | Hot-reloadable, no PR needed                                   | Not version-controlled, can drift        |

**Recommendation:** YAML config in the repo. The mapping changes infrequently (only when teams reorganize), and version control + PR review is more important than hot-reloadability.

## Routing Mechanism Options

### Option A: Release Dashboard (post-classification hook)

The Release Dashboard already calls `hcpctl snapshot analyze` for gating runs and displays the results. Add a post-analysis step that reads the classification and creates/updates a Jira ticket with the appropriate component.

|              |                                                                                                                                                            |
|--------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Pros**     | Closest to where classification already happens; single integration point for gating runs                                                                  |
| **Cons**     | Cross-tenant (lives in `sdp-pipelines`, Microsoft repo); ARO-HCP team has limited control over release cadence; only covers gating runs until batch lands  |
| **Coupling** | Medium — depends on cross-repo changes, but ARO-HCP engineers have direct access                                                                           |

### Option B: Batch analyzer post-processing

Add a routing step to the Tide batch analyzer (ARO-28327) after it produces classification output.

|              |                                                                                                                                                            |
|--------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Pros**     | Co-located with analysis; covers batch runs natively; runs in RH tenant                                                                                    |
| **Cons**     | Adds scope to ARO-28327 (Roi's work); doesn't cover gating runs (those go through Release Dashboard); creates two routing paths                            |
| **Coupling** | Medium — adds dependency on batch analyzer timeline                                                                                                        |

### Option C: Jira automation rules

Configure Jira automation rules that trigger when a taxonomy label (e.g., `taxonomy:product:frontend`) is applied to an issue. The rule sets the Jira component and optionally assigns to a team lead.

|              |                                                                                                                                                            |
|--------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Pros**     | Zero code; decoupled from analysis pipeline; covers all run types uniformly; easy to modify                                                                |
| **Cons**     | Relies on labels being applied correctly upstream; Jira automation has limited logic; not version-controlled; debugging is opaque                          |
| **Coupling** | Low — only needs labels to be applied consistently                                                                                                         |

> **⚠ Concern:** Jira automation rules are not currently used anywhere in the ARO project. A search of all ARO Jira issues (JQL for "automation rule"/"jira automation" in summary and description) and Confluence (Rovo search) returned zero ARO-specific results — the only Jira automation usage at Red Hat is in unrelated projects like CapEx PGE. Adopting them introduces a net-new operational surface that the team has no experience maintaining or debugging.

### Option D: CIHealth auto-triage flow

CIHealth already ingests CI run data from Sippy/Prow. Extend it to consume `analysis.json` from the analysis pipeline and create/route Jira tickets based on classification.

|              |                                                                                                                                                            |
|--------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Pros**     | CIHealth is already the CI health hub; single routing path for all run types; runs in RH tenant                                                            |
| **Cons**     | CIHealth is an external app (not in-repo); requires CIHealth to consume analysis output it doesn't currently read; adds Jira write capability to CIHealth  |
| **Coupling** | Medium — depends on CIHealth changes                                                                                                                       |

> **⚠ Concern:** CIHealth's triage functionality is actively being consolidated into the Release Dashboard. Building routing on CIHealth means building on a foundation that is migrating — any work here would need to be re-done in the Release Dashboard once the consolidation completes.

### Option E: Hybrid — Jira automation + config-driven labeling

Separate the problem into two parts:
1. **Labeling** — the analysis pipeline (Release Dashboard, batch analyzer) applies taxonomy labels to Jira tickets as part of its output. This is just `POST /label`.
2. **Routing** — Jira automation rules react to labels and set component/assignee. The rules are configured in Jira but documented in the repo.

|              |                                                                                                                                                            |
|--------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Pros**     | Clean separation of concerns; labeling is simple (each consumer just adds labels); routing logic is centralized in Jira; easy to adjust routing w/o deploy |
| **Cons**     | Two moving parts (labeling + rules); Jira automation not version-controlled (mitigated by documenting rules in repo)                                       |
| **Coupling** | Low — each analysis consumer just needs to apply labels                                                                                                    |

## Recommendation

**Option A (Release Dashboard)** is the recommended approach.

### Why the original recommendation (Option E) was revised

Option E (Hybrid) relied on Jira automation rules for the routing half. Two findings invalidate that approach:

1. **Jira automation is a net-new capability.** No existing ARO project workflows use Jira automation rules. Adopting them introduces an unfamiliar, non-version-controlled operational surface with opaque debugging. The "zero code" benefit is offset by "zero visibility."

2. **CIHealth triage is being consolidated into the Release Dashboard.** Option D (CIHealth auto-triage) and any hybrid that depends on CIHealth's triage flow would be building on a foundation that is actively migrating. Option E's labeling step is still sound, but without Jira automation as the routing mechanism, it reduces to just a labeling convention without a consumer.

### Why Option A

The Release Dashboard already runs `hcpctl snapshot analyze` for gating runs and displays results. It is also the destination for CIHealth's migrating functionality, making it the convergence point for all CI health and triage tooling. Routing in the Release Dashboard means:

1. **Single integration point.** Classification and routing happen in the same system, avoiding the split-brain problem of labeling in one place and routing in another.

2. **Covers gating runs immediately.** The Release Dashboard already processes these. Once the batch analyzer (ARO-28327) lands, it can feed into the same routing path.

3. **Alignment with platform direction.** As CIHealth migrates into the Release Dashboard, routing built there won't need to be re-done.

4. **Version-controlled routing config.** The L2→team mapping can live as a YAML config in the `sdp-pipelines` repo (or in ARO-HCP and consumed by the dashboard), keeping it PR-reviewed and auditable.

### Trade-offs

The main downside is **cross-repo coupling**: the Release Dashboard lives in `sdp-pipelines` (Microsoft repo). ARO-HCP engineers have access and can implement changes directly, but PRs still need review from the dashboard team and follow their release cadence. This is the same cost the project already pays for gating analysis — adding routing is incremental, not a new dependency.

### Alternative: revisit Option E if Jira automation becomes viable

Option E (Hybrid) is architecturally simpler than Option A — each analysis consumer just applies labels, and Jira automation handles all routing centrally. There's no cross-tenant coupling, no coordination with the dashboard team, and routing rules can be changed without any code deploy. The only reason it's not recommended today is that Jira automation is net-new for the ARO project and the team has no operational experience with it.

If the team decides the Jira automation learning curve is acceptable (or if another ARO team adopts it first and establishes patterns), Option E becomes the preferred approach. It's a lower-coupling, lower-coordination solution that scales naturally as new analysis consumers come online.

### Labeling Convention

Taxonomy labels on Jira tickets follow the existing convention established on [ARO-28136](https://redhat.atlassian.net/browse/ARO-28136):

- `taxonomy:<category>` — L1 category (e.g., `taxonomy:azure`, `taxonomy:product`, `taxonomy:deployment`, `taxonomy:test-reliability`)
- `taxonomy:product:<component>` — L2 subcategory, only for Product Failures (e.g., `taxonomy:product:frontend`, `taxonomy:product:cs`, `taxonomy:product:hypershift`)
- `taxonomy:needs-investigation` — default for failures without LLM analysis (per [ARO-28141](https://redhat.atlassian.net/browse/ARO-28141))

### Routing Rules

The Release Dashboard applies labels and sets the Jira component directly based on the L2→team mapping config:

| L2 Subcategory  | Jira Component (proposed)  | Exists? | Additional Action                |
|-----------------|----------------------------|---------|----------------------------------|
| Frontend        | `aro-hcp-frontend`         | ❌ New  |                                  |
| Backend         | `aro-hcp-backend`          | ❌ New  |                                  |
| Cluster Service | `aro-hcp-cluster-service`  | ✅ Yes  |                                  |
| Maestro         | `aro-hcp-maestro`          | ❌ New  |                                  |
| HyperShift      | `HyperShift`               | ✅ Yes  |                                  |
| RH Upstream     | `aro-hcp-upstream`         | ❌ New  | Add `taxonomy:needs-upstream-triage` |
| (low confidence)| (per L2)                   | —       | Add `taxonomy:needs-manual-review`   |

> **Note:** The existing `aro-hcp-1p` component covers "First Party services that make ARO part of Microsoft Azure cloud" — this may already serve as the umbrella for Frontend and Backend. Consider whether to create separate `aro-hcp-frontend`/`aro-hcp-backend` components or reuse `aro-hcp-1p` and differentiate via labels.

### Rollout Plan

1. **Now (no dependency):** Define labeling convention and routing rules in this doc. Create the Jira components if they don't exist.
2. **After ARO-28427 merges:** Classification data is available in `analysis.json`. Manual verification with local runs.
3. **Add routing logic to the Release Dashboard:** Implement a post-analysis step that reads `classification` from `analysis.json`, applies labels, and sets the Jira component. ARO-HCP engineers have access to `sdp-pipelines` and can implement this directly, though coordination with the dashboard team is still recommended for review and release.
4. **After ARO-28327 lands:** Wire the batch analyzer's output into the same routing path.
5. **After 2-week bake:** Review routing accuracy, adjust mapping as needed.

## Existing Jira Components in the ARO Project

Queried via Jira API (Sep 2026). Components currently in use:

| Component | ID | Description |
|---|---|---|
| `ARO` | 37596 | Cross-cutting (ARO HCP + Classic) |
| `ARO HCP` | 37586 | General ARO HCP |
| `ARO Classic` | 37584 | ARO Classic |
| `aro-hcp-1p` | 37590 | First Party services (frontend/backend umbrella) |
| `aro-hcp-clusters-service` | 37580 | Clusters Service (overall) |
| `aro-hcp-clusters-service-east` | 37585 | Clusters Service (east team) |
| `aro-hcp-capz` | 37592 | CAPZ team |
| `aro-hcp-ci` | 37598 | CI tiger team |
| `aro-hcp-qe` | 37583 | QE team |
| `HyperShift` | 86825 | HyperShift |
| `aro-pmr` | 84805 | PMRs |

Key observations:
- `aro-hcp-clusters-service` and `HyperShift` already exist and map directly to L2 subcategories.
- `aro-hcp-1p` covers both frontend and backend — separate `aro-hcp-frontend` / `aro-hcp-backend` components do not exist. Decide whether to create them or reuse `aro-hcp-1p`.
- `aro-hcp-maestro` and `aro-hcp-upstream` do not exist and would need to be created.
- No Jira automation rules exist in the ARO project (confirmed via JQL and Rovo search).

## Open Questions

- [ ] Should Frontend and Backend get their own Jira components, or should `aro-hcp-1p` be reused? (Separate components give finer routing; reusing avoids component sprawl.)
- [ ] Who are the actual triage owners per L2 subcategory? (The "Team / Triage Owner" column in the mapping table needs to be filled in — OWNERS file approvers are architects, not component owners.)
- [ ] Should low-confidence classifications be auto-routed at all, or should they go to a triage queue first?
- [ ] For non-Product-Failure L1 categories (Azure Problems, Deployment Failures, Test Reliability), is there a default routing target? Currently only Product Failures have L2 subcategories.
- [ ] What is the Release Dashboard team's appetite for adding routing logic? What is their release cadence for changes?
- [ ] Should a `taxonomy:confidence:<band>` label (e.g., `taxonomy:confidence:high`, `taxonomy:confidence:low`) be added to the convention? This would make confidence queryable via JQL and could drive `taxonomy:needs-manual-review` for low-confidence classifications. Currently the confidence value only lives in `analysis.json`.

## Follow-Up Implementation Ticket

**Title:** Implement taxonomy-based auto-routing via Release Dashboard

**Acceptance Criteria:**
- Release Dashboard reads `classification` from `analysis.json` after running `hcpctl snapshot analyze`
- Taxonomy labels (`taxonomy:*`, `taxonomy:product:*`) are applied to Jira tickets
- Jira component is set based on the L2→team mapping config
- Low-confidence classifications are flagged with `taxonomy:needs-manual-review`
- L2→team mapping is version-controlled and configurable without code changes
- Routing rules are documented in the repo

**Depends on:** Coordination with Release Dashboard team; ARO-28427 merged
