---
name: create-jira-issue
description: Create a well-formed JIRA issue in the AROSLSRE project with correct fields, labels, components, sprint assignment, and JIT documentation when applicable. Follows ARO HCP JIRA governance conventions.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

# Create JIRA Issue

## What I Do

Create JIRA issues in the **AROSLSRE** (ARO HCP Service Lifecycle) project with
correctly populated fields: issue type, priority, component(s), labels, sprint
assignment, sizing, and a structured description. Follows the official
[ARO HCP JIRA governance conventions][governance-doc] for hierarchy, workflows,
definitions of ready/done, and field requirements.

When Just-In-Time (JIT) cluster access is involved, I guide the user through
documenting the JIT request per team conventions.

[governance-doc]: https://docs.google.com/document/d/1jLwHt00p5EyW4hYIUlDleQGh0K96UfZrmcp-BJ3BmaM

## When to Use Me

- CI or Prow job failures that need tracking
- e2e-parallel or e2e test failures
- EV2 rollout or promotion failures
- Oncall investigation items
- Any interrupt-driven bug or task for the Service Lifecycle team
- Documenting JIT access requests and outcomes
- Creating Epics, Stories, Bugs, or Spikes per team process

## JIRA Hierarchy

The ARO HCP space uses a layered hierarchy. This skill primarily creates
**Level 2** items (Task, Story, Bug, Spike) and occasionally **Level 3** (Epic).
Higher levels (Feature/Initiative, Outcome) are managed by PMs/EMs.

```
Outcome (L5)  →  Feature/Initiative (L4)  →  Epic (L3)  →  Story/Task/Bug/Spike (L2)
   Planning spaces (HCMSTRAT, OCPSTRAT)       Execution spaces (AROSLSRE, ARO, HOSTEDCP)
```

## Parameters

The agent should collect (or infer from context) the following before creating
an issue:

| Parameter | Required | Description |
|-----------|----------|-------------|
| `summary` | yes | One-line summary of the issue |
| `description` | yes | Detailed description (see description template below) |
| `failure_source` | yes | What failed — guides component and label selection |
| `issue_type` | no | Task (default for investigations), Bug (confirmed defects), Story (user-facing work), Spike (time-boxed research) |
| `priority` | no | Blocker / Critical / Major / Normal / Minor (default: Normal) |
| `size` | no | Story points: 1, 2, 3, 5, 8, 13, 20 (see sizing guide) |
| `parent_epic` | no | Parent Epic key if this task belongs to an Epic |
| `jit_involved` | no | Whether JIT cluster access was requested or used |
| `additional_labels` | no | Extra labels beyond auto-applied ones |

## Directions

### Step 1 — Choose the Right Issue Type

Select based on the nature of the work. The governance doc defines these precisely:

| Issue Type | When to Use | ID |
|------------|-------------|-----|
| **Task** | Finite piece of work. Oncall investigations, post-meeting follow-ups, action items. Most common for oncall. | 10014 |
| **Bug** | A defect — error, flaw, or fault causing incorrect/unexpected results. Requires triage before development. | 10016 |
| **Story** | A short description of a capability from the perspective of the person who desires it. User-facing work. | 10009 |
| **Spike** | Time-boxed research to help the team make better decisions. Use for investigatory work with unclear scope. | 10009 |
| **Epic** | Extra-large work that won't fit in a single sprint. Includes acceptance criteria from end-user perspective. | 10000 |
| **Sub-task** | Child of an existing Task, Story, or Bug. | 10015 |

> **Note**: Spikes use the Story issue type with a `spike` label to distinguish
> them. They should have a clear time-box defined in the description.

### Step 2 — Determine the Current Sprint

Query for the current active sprint so the issue lands in the right iteration:

```
Tool: mcp_jira_searchJiraIssuesUsingJql
cloudId: 2b9e35e3-6bd3-4cec-b838-f4249ee02432
jql: project = AROSLSRE AND sprint in openSprints() ORDER BY created DESC
maxResults: 1
fields: ["customfield_10020"]
```

Extract the sprint **integer ID** from `customfield_10020` in the first result.
The value is an array of sprint objects — use the `id` field from the active one.

> **Gotcha**: The sprint field accepts a **bare integer**, NOT an object.
> Passing `{"id": 67024}` will fail with _"Number value expected as the Sprint
> id."_ — pass `67024` directly.

### Step 3 — Select Component(s)

**Always include `ARO-HCP` as a component** (per governance doc: "Please use the
'ARO-HCP' component on your ARO JIRAs"). Then add a team/area-specific component
based on the failure source.

Add `aro-hcp-oncall` (84116) as a secondary component for any oncall-driven
investigation.

| Failure Source | Primary Component | ID |
|---------------|-------------------|----|
| e2e-parallel, e2e test failures | aro-hcp-e2e | 83786 |
| Prow CI, PR pipeline, CI stability | aro-hcp-ci | 32027 |
| EV2 rollout, stage/prod deploy, promotion | aro-hcp-releases | 83787 |
| AKS cluster, nodepool, istio, infra | aro-hcp-infra | 84112 |
| Alerts, dashboards, logging, metrics | aro-hcp-observability | 84111 |
| CVEs, dependency bumps, security | aro-hcp-security | 84113 |
| Tooling, image updater, CLI tools | aro-hcp-tooling | 84114 |
| Docs, runbooks, onboarding | aro-hcp-docs | 84115 |

**Team board routing** (determines which board the ticket appears on):

| Team/Board | Component to Add |
|------------|-----------------|
| Service Lifecycle Dashboard | `aro-hcp-service-lifecycle` |
| ARO HCP - First Party | `aro-hcp-1p` |
| ARO HCP - Cluster Service East | `aro-hcp-clusters-service-east` |
| ARO HCP - Cluster Service West | `aro-hcp-clusters-service-west` |
| ARO HCP - QE | `aro-hcp-qe` |
| ARO HCP - CI Tiger Team | `aro-hcp-ci` |

Format components as an array of objects: `[{"id": "83786"}, {"id": "84116"}]`

### Step 4 — Select Labels

**Always apply these automatically based on context:**

| Condition | Label | Notes |
|-----------|-------|-------|
| CI failure, e2e failure, promotion failure, rollout failure, oncall investigation | `oncall` | Lowercase. Do NOT use `on-call`. |
| JIT cluster access requested or used | `JIT` | Uppercase. |
| e2e test specific | `e2e` | In addition to `oncall` |
| No QE testing needed | `no-qe` | QE assumes every ticket needs testing unless this label is present |
| Suitable for new team members | `good-first-issue` | For straightforward onboarding-friendly tasks |
| Microsoft backlog item | `team-msft-backlog` | For items tracked by Microsoft |

Other labels to consider (apply when relevant):
- `maestro` — Maestro-specific issues
- `aro-hcp-ci` — CI pipeline issues
- `ARO-HCP` — generic project label
- `aro-hcp-service-lifecycle-team` — team label
- `spike` — for Spike (time-boxed research) issues

### Step 5 — Set Priority

Priority maps to MoSCoW prioritization per the governance doc:

| Impact | Priority | ID | MoSCoW |
|--------|----------|-----|--------|
| Service down, blocking rollout | Blocker | 10000 | Must Have |
| Major feature broken, data loss risk | Critical | 10001 | Should Have |
| Significant degradation | Major | 10002 | Could Have |
| Standard work, intermittent failures | Normal | 10003 | Could Have |
| Cosmetic, low impact | Minor | 10004 | Won't Have |

### Step 6 — Size the Issue (Story Points)

If the user provides sizing context, set `customfield_10028` (Story Points).
Use the team's sizing guide:

| Size | Uncertainty | Effort | Complexity | Scope |
|------|-------------|--------|------------|-------|
| 1 | None | Very low | Very low | Single component |
| 2 | Very low | Very low/low | Very low/low | 1-2 components |
| 3 | Low | Low | Low | Multiple components |
| 5 | Low/medium | Medium | Medium | Multiple components |
| 8 | Medium/high | Medium/high | Medium/high | Multiple components |
| 13 | High | High | High | Consider splitting |
| 20+ | Very high | Very high | Very high | Split into Epic or smaller items |

> If the user is unsure about sizing, suggest creating the issue unsized and
> letting the team point it during refinement.

### Step 7 — Compose the Description

Use markdown format (`contentFormat: "markdown"`). The governance doc requires
these sections in all descriptions:

```markdown
## Overview

<One-paragraph summary of what happened and why it matters.>

## Acceptance Criteria

- <Criterion 1: specific, testable condition>
- <Criterion 2>

## Definition of Done

- [ ] All acceptance criteria met
- [ ] Required tests passing (unit, integration, e2e as applicable)
- [ ] CI and all relevant tests passing
- [ ] PR reviewed and merged
- [ ] Changes verified against each required environment
- [ ] Code rolled out to all production regions (if not part of an Epic)

## Evidence

- **Failed job/test**: <URL or name>
- **Error**: <Key error message or log snippet>
- **Environment**: <int / stg / prod, region if relevant>
- **Frequency**: <First occurrence / intermittent / persistent>

## Investigation Log

Each investigation step MUST be recorded as a structured entry with all four
fields. This ensures reproducibility and allows other engineers (and bots) to
verify findings without re-doing the work.

### Entry format

For each query, command, or check performed:

| Field | Required | Description |
|-------|----------|-------------|
| **Command/Query** | yes | The exact command, KQL query, or kubectl command run. Use code blocks. |
| **Result** | yes | The output — a summary table, key numbers, or "empty/no results". Truncate verbose output but preserve key data. |
| **Summary** | yes | One-sentence interpretation: what does this result mean for the investigation? |
| **Reproduce** | yes | A link or instructions to re-run: ADX deep-link for Kusto queries, full kubectl command with context/namespace, or Prow artifact URL. |

### Example entry

```
### Q1: KMS container events in CI namespaces

**Command**:
\`\`\`kql
database('ServiceLogs').kubernetesEvents
| where eventNamespace startswith "ocm-arohcpci01-"
| where objectName has "kms" or message has "kms"
| summarize count() by category
\`\`\`

**Result**:
| Category | Count | Namespaces |
|----------|-------|------------|
| KMS_MountFailure | 45526 | 39267 |
| KMS_ContainerKill | 27171 | 12380 |

**Summary**: KMS cert mount failures are ubiquitous across CI — the
SecretProviderClass `managed-azure-kms` is transiently unavailable at
pod startup in nearly every namespace.

**Reproduce**: [Open in ADX Web](https://dataexplorer.azure.com/...)
```

When posting investigation comments to JIRA, use `mcp_jira_addCommentToJiraIssue`
with `contentFormat: "markdown"` and format each step as above.

## References

- <Prow job URL>
- <Slack thread>
- <Related issues>
```

**For Bugs specifically**, also include:
- Procedure to reproduce the problem
- Who will test / how to verify the fix
- Expected vs. actual behavior

**For Epics**, the description should include:
- Functionality from the end-user perspective
- Critical child tasks/stories identified
- T-shirt size estimate

### Step 8 — Create the Issue

```
Tool: mcp_jira_createJiraIssue
cloudId: 2b9e35e3-6bd3-4cec-b838-f4249ee02432
projectKey: AROSLSRE
issueTypeName: Task          # or Bug, Story, Epic
summary: <summary>
description: <description>
contentFormat: markdown
additional_fields:
  priority:
    name: Normal             # or Blocker, Critical, Major, Minor
  labels:
    - oncall
    - JIT                    # only if JIT involved
    - no-qe                  # only if no QE testing needed
  components:
    - id: "83786"            # primary component
    - id: "84116"            # secondary if oncall
  customfield_10020: 67024   # current sprint ID (bare integer!)
  customfield_10028: 3       # story points (optional)
  customfield_10014: AROSLSRE-XXX  # parent Epic (optional)
```

### Step 9 — Transition to the Correct Status

New issues start in **New** status. Transition based on readiness:

| Scenario | Transition | ID |
|----------|-----------|-----|
| Actively investigating now | In Progress | 51 |
| Triaged, ready for development | To Do | 41 |
| Triaged, blocked by other work (Bugs) | Backlog | 21 |
| Needs refinement/discussion first | Refinement | 31 |

```
Tool: mcp_jira_transitionJiraIssue
cloudId: 2b9e35e3-6bd3-4cec-b838-f4249ee02432
issueIdOrKey: AROSLSRE-XXX
transition:
  id: "51"                   # In Progress
```

**Workflow by issue type** (from governance doc):

**Tasks**: New → Backlog → To Do → In Progress → Review → Closed
- "To Do" means Definition of Ready is met
- "Review" means code review stage
- "Closed" means Definition of Done met and PR merged

**Bugs**: New → Backlog → To Do → In Progress → Review → Closed
- "Backlog" can mean triaged but blocked by upstream fix
- "To Do" means bug is validated, testable, and has proposed fix target
- "Review" with QA contact set means ready for QE verification
- If QE testing fails, move back to "In Progress"

**Epics**: New → Refinement → Backlog → In Progress → Review → Closed
- "Refinement" means description and child issues being written
- "Backlog" means refinement complete, ready for development
- "Closed" requires all child Tasks to be Closed

### Step 10 — Handle JIT Documentation (if applicable)

When `jit_involved` is true (or JIT access is mentioned anywhere in the
context), **remind the user** to provide the following and add them as comments
on the issue:

#### JIT Request Comment

```markdown
## JIT Request

- **JIT Request URL**: https://jitaccess.security.core.windows.net/...
- **Rollout URL**: <if applicable>
- **Slack Discussion**: <link to Slack thread>
- **Reason**: <Why JIT access was necessary>
```

#### JIT Access Details Comment

```markdown
## JIT Access Details

- **JIT Approver**: <Name> (<email>)
- **Shadow**: <Name> (<email>)
- **Performed By**: <Name> (<email>)
```

#### JIT Duration Comment

```markdown
## JIT Duration

- **Approved**: <YYYY-MM-DD HH:MM UTC>
- **Completed**: <YYYY-MM-DD HH:MM UTC>
- **Total Duration**: <Xh Ym>
```

#### JIT Outcome Comment

```markdown
## JIT Outcome

<What was found during JIT access. What actions were taken.
Any corrections to initial assumptions or hypotheses.>
```

Use `mcp_jira_addCommentToJiraIssue` with `contentFormat: "markdown"` for each
comment block.

**Checklist — ask the user for any missing items:**
- [ ] JIT request URL
- [ ] Rollout URL (if applicable)
- [ ] Slack thread link
- [ ] Approver name and email
- [ ] Shadow name and email
- [ ] Performer name and email
- [ ] What was done and found (outcome)
- [ ] Start and end time of JIT session

## Definition of Ready Checklist

Before moving an issue to **To Do**, verify these criteria are met (from
governance doc):

- [ ] Priority field is set
- [ ] Description has sufficient information for a developer to start
- [ ] Story points / size determined
- [ ] Ranked in backlog
- [ ] Acceptance Criteria defined
- [ ] Not blocked (`customfield_10517` = False)
- [ ] Components set (at minimum `ARO-HCP` + team component)
- [ ] Labels set (`no-qe` if QE testing not required)

## Definition of Done Checklist

Before closing an issue, verify (from governance doc):

- [ ] All acceptance criteria met
- [ ] Required tests passing (unit, integration, e2e, manual as relevant)
- [ ] Required documentation shipped
- [ ] Required metrics, dashboards, and alerts in place
- [ ] Required SOPs written
- [ ] CI and all relevant tests passing
- [ ] Changes verified by reviewer against each required environment
- [ ] Changes verified against each supported upgrade path
- [ ] If client impact: JIRA created for client-side changes
- [ ] PR merged
- [ ] Code rolled out to all production regions (if not part of an Epic)

## Reference Tables

### Project

| Field | Value |
|-------|-------|
| Project Key | AROSLSRE |
| Project ID | 10555 |
| Project Name | ARO HCP Service Lifecycle |
| Cloud ID | 2b9e35e3-6bd3-4cec-b838-f4249ee02432 |
| Instance | redhat.atlassian.net |

> **Note**: There is no separate "ARO-HCP" project accessible via the API.
> All ARO HCP Service Lifecycle work goes to **AROSLSRE**.

### All AROSLSRE Components

| Name | ID | Use For |
|------|----|---------|
| aro-hcp-ci | 32027 | Prow jobs, PR pipelines, CI stability |
| aro-hcp-docs | 84115 | Runbooks, onboarding guides |
| aro-hcp-e2e | 83786 | e2e test failures, flakes, resource cleanup |
| aro-hcp-infra | 84112 | AKS clusters, nodepools, istio, capacity |
| aro-hcp-observability | 84111 | Alerts, dashboards, Kusto, logging |
| aro-hcp-oncall | 84116 | Oncall rotation, interrupt-driven work |
| aro-hcp-releases | 83787 | EV2 rollouts, deployments, image bumping |
| aro-hcp-security | 84113 | CVEs, dependency bumps, FedRAMP |
| aro-hcp-tooling | 84114 | Image updater, CLI tools, agentic workflows |

### All Issue Types

| Name | ID | Level |
|------|----|-------|
| Task | 10014 | 2 |
| Bug | 10016 | 2 |
| Story | 10009 | 2 |
| Spike | 10009 | 2 (Story + `spike` label) |
| Epic | 10000 | 3 |
| Sub-task | 10015 | child |
| Feature | 10142 | 4 |
| Vulnerability | 10172 | 2 |

### Priority Values (MoSCoW Mapping)

| Name | ID | MoSCoW |
|------|----|--------|
| Blocker | 10000 | Must Have |
| Critical | 10001 | Should Have |
| Major | 10002 | Could Have |
| Normal | 10003 | Could Have |
| Minor | 10004 | Won't Have |

### Status Transitions

| ID | Name | Target Status |
|----|------|---------------|
| 11 | New | New (To Do) |
| 21 | Backlog | Backlog |
| 31 | Refinement | Refinement |
| 41 | To Do | To Do |
| 51 | In Progress | In Progress |
| 61 | Review | Review |
| 71 | Release Pending | Release Pending (Done) |
| 81 | Closed | Closed (Done) |

### Notable Custom Fields

| Field | Key | Value Type | Notes |
|-------|-----|------------|-------|
| Sprint | customfield_10020 | bare integer | **Not** an object |
| Story Points | customfield_10028 | number | 1, 2, 3, 5, 8, 13, 20 |
| Epic Link | customfield_10014 | string (issue key) | Parent Epic |
| Severity | customfield_10840 | string | Critical/Important/Moderate/Low/Informational |
| Release Blocker | customfield_10847 | string | Approved/Proposed/Rejected |
| Blocked | customfield_10517 | string | True/False (default False) |
| Target Start | — | date | When dev work actually begins |
| Target End | — | date | When work completes and ships to prod |
| Color Status | — | string | Green (on track) / Yellow (at risk) / Red (off track) |
| Status Summary | — | text | Summarized status, risks, mitigations |
| Fix Version | — | string | Used for planning (especially Bugs) |

## Technical Notes

1. **Sprint field** (`customfield_10020`) accepts a **bare integer**, not an
   object. `67024` works; `{"id": 67024}` fails.

2. **Cloud ID** is `2b9e35e3-6bd3-4cec-b838-f4249ee02432` — hardcoded for the
   Red Hat Atlassian instance.

3. **Content format**: Always use `contentFormat: "markdown"` for descriptions
   and comments.

4. **Label casing**: `oncall` (lowercase), `JIT` (uppercase). These are the
   established conventions in the project.

5. **Component format**: Pass as `[{"id": "83786"}]` — the `id` value is a
   string representation of the integer.

6. **Sprint lookup**: Always query for the current sprint at creation time
   rather than hardcoding a sprint ID, as sprints rotate every two weeks.

7. **`no-qe` label**: QE assumes every ticket needs testing unless this label
   is present. Apply it for pure infrastructure, tooling, or internal-only
   changes that don't need QE verification.

8. **Bug triage**: Bugs follow a separate triage process. New bugs should stay
   in **New** until the team agrees: (a) the bug is valid, (b) there's enough
   detail to reproduce, (c) testing procedure is defined, (d) testing owner
   assigned. Only then transition to **To Do**.

9. **Epics require a parent**: Per governance, Epics should have a parent
   Feature or Initiative. If creating an Epic, ask the user for the parent.

10. **Color Status for tracked work**: For Features/Epics being tracked at
    the program level, update weekly with Color Status (Green/Yellow/Red) and
    a Status Summary including risks and mitigations.

## MCP Tools Reference

| Tool | Purpose |
|------|---------|
| `mcp_jira_createJiraIssue` | Create the issue |
| `mcp_jira_addCommentToJiraIssue` | Add JIT or investigation comments |
| `mcp_jira_transitionJiraIssue` | Change issue status |
| `mcp_jira_searchJiraIssuesUsingJql` | Look up current sprint, find related issues |
| `mcp_jira_getTransitionsForJiraIssue` | List available transitions |
| `mcp_jira_getJiraIssueTypeMetaWithFields` | Inspect field metadata |
| `mcp_jira_getVisibleJiraProjects` | Project lookup |
| `mcp_jira_createIssueLink` | Link related issues (Blocks, Duplicate, etc.) |
