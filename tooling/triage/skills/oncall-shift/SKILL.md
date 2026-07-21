---
name: oncall-shift
description: Manage an oncall shift end-to-end — accept handover, prioritize tasks, track work via JIRA with the oncall label, and generate structured handover notes for the next shift. Use whenever a user starts an oncall shift, receives handover notes, wants to update shift status, or needs to create handover notes.
---

## Personal Overrides

If a `SKILL.local.md` file exists in this skill's directory, read it before
proceeding. It contains personal instructions that augment (never contradict)
the directions below. These files are gitignored and persist across upstream
skill updates.

# Oncall Shift Management

## What I Do

Manage the full lifecycle of an oncall shift:

1. **Accept handover** — parse incoming handover notes (Slack pastes, text),
   extract action items, and create a prioritized plan.
2. **Create tracking JIRA** — create or update a shift-level JIRA ticket that
   tracks all work during the shift.
3. **Prioritize tasks** — order by: rollouts first, then time-sensitive /
   important items, then alerts, then routine monitoring.
4. **Track updates** — add JIRA comments as work progresses throughout the
   shift, preserving chat excerpts with attribution.
5. **Generate handover notes** — produce structured handover notes for the next
   shift in a consistent format.

## Coverage Model

The team follows a **24x5 Follow-the-Sun (FTS)** model.

- **Coverage window**: Sunday 21:00 UTC to Friday 21:00 UTC
- **Weekend policy**: No weekend SLAs. Pager rotations and active monitoring
  pause on Saturdays and Sundays.
- **Shift handover**: Each region (APAC → EMEA → NASA) hands over IC
  responsibilities at the end of their local business day.

### Shift Schedule

| GEO | Start (UTC) | End (UTC) |
|-----|-------------|----------|
| APAC East | 22:00 | 04:00 |
| APAC West | 04:00 | 10:00 |
| EMEA | 10:00 | 16:00 |
| NASA | 16:00 | 22:00 |

Scheduling is managed quarterly via IcM. Check your schedule at
[IcM / MyOnCall](https://portal.microsofticm.com/).

## Roles

Each shift has a **Primary** and **Secondary** responder.

### Primary Responder (Releases & Alerts)

The Primary is the Interrupt Collector (IC). Responsibilities:

1. **Release management** — execute all releases across INT, Stage, and Prod
   per the continuous, serialized rollout model.
2. **Quality Service Review (QSR)** — attend the QSR meeting to provide
   release updates.
3. **Proactive revert policy** — if a component breaks a rollout or fails a
   gate, perform an immediate revert. Before reverting, verify the failure is
   not infrastructure or pipeline related. Once reverted, notify the
   responsible team to provide a fix for re-submission in a later batch.
4. **IcM alert management** — see IcM Alert Triage section below.
5. **Dev merge queue health** — see Dev Merge Queue Monitoring section below.

### Secondary Responder (Communication & Triage)

*Not currently enforced — team-based best effort.*

- **Slack monitoring**: First point of contact for all IC pings in designated
  Slack channels.
- **Tag response**: Respond to all team @-mentions and tags related to service
  lifecycle issues.

## When to Use Me

- User starts an oncall shift and pastes or describes handover notes
- User wants to create a prioritized oncall plan
- User wants to log an update to their oncall JIRA
- User wants to generate handover notes for the next shift
- User mentions "oncall", "handover", "shift tracking", or "on-call"

## Core Principles

1. **If it's not in the handover notes, it didn't happen.** Every action,
   decision, and status change must be recorded — either in the JIRA ticket
   or in the handover notes. Verbal-only updates are lost.

2. **Every JIRA ticket created during oncall must have the `oncall` label.**
   This is non-negotiable. It enables filtering oncall interrupt work from
   planned sprint work.

3. **Chat excerpts must include attribution.** When recording Slack messages
   or conversations, always capture: **who** said it, **when** (timestamp),
   and **where** (Slack link if available). Ask for the Slack link if not
   provided.

4. **Prioritization order is fixed:**
   1. Rollouts (active, failed, blocked)
   2. Time-sensitive / important items (CCOA exceptions, approvals, deadlines)
   3. Dev merge queue health (unblock Tide batches)
   4. Check alerts (proactive problem detection)
   5. Routine monitoring (IcM, Prow, dashboards, quality call)

5. **IcM alerts have a hard SLA.** All Sev 2 and below critical alerts must
   be acknowledged within 30 minutes. This is not optional.

## Parameters

Extract from user context. Only ask if required and not inferrable.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `handover_notes` | yes (for start) | Raw handover text — Slack paste, typed notes, etc. |
| `shift_name` | no | Timezone shift name (EMEA, NASA, APAC). Infer from context. |
| `engineer` | no | Oncall engineer's name. Infer from git config or context. |
| `tracking_jira` | no | Existing JIRA key if resuming a shift. Created automatically if not provided. |

## Directions

### Phase 1 — Accept Handover & Create Plan

When the user starts a shift (pastes handover notes or describes context):

#### Step 1: Parse Handover Notes

Extract from the raw input:

- **Rollouts** — active, completed, failed, blocked. Include EV2 links.
- **Action items** — things explicitly assigned or requested.
- **Awareness items** — FYIs, no action needed but carry forward.
- **PRs to babysit** — PRs that need merging or monitoring.
- **Incidents** — IcM incidents, capacity issues, outages.
- **Chat excerpts** — preserve who said what and when. Flag any item
  where a Slack link is missing and ask for it.

For each chat excerpt, enforce this format:

```
- **<Person> [<time>]**: <message> ([Slack](<link>))
```

If the Slack link is not provided, record without it but flag:

```
- **<Person> [<time>]**: <message> *(Slack link not provided — ask sender)*
```

#### Step 2: Prioritize

The five categories below are standing responsibilities every shift. All
five must appear in every action plan regardless of what the handover notes
contain. Handover items, action items, PRs to babysit, and awareness items
are slotted into whichever category they belong to — they do not form a
separate list.

1. **🔴 Rollouts** — Active rollouts, failed rollouts needing cancellation,
   blocked rollouts. Slot handover rollout items here.
2. **🟠 Important / Time-Sensitive** — CCOA exceptions, approval chains,
   PRs with deadlines, pipeline recoveries. Slot handover action items, PRs
   to babysit, and time-sensitive requests here.
3. **🟡 Dev Merge Queue** — Check Tide batch health and unblock if needed
   (see Dev Merge Queue Monitoring section below). Always present even if
   handover does not mention it.
4. **🟡 Alerts** — Check monitoring alerts for proactive problem detection.
   Always present even if handover does not mention it.
5. **🟢 Routine Monitoring** — IcM portal sweep, oncall dashboard ticket
   triage, Prow status, quality call, handover dashboard. Slot handover
   awareness items and incidents here. Always present even if handover does
   not mention it.

If the handover notes contain items that already match a standing category
(e.g. an IcM incident is routine monitoring, a PR deadline is
important/time-sensitive), place them in that category rather than creating
duplicates.

Present as a numbered action sequence (not just categories). Every category
must have at least one concrete action, even if it is the standing default:

```
### Suggested Sequence

1. <Most urgent rollout action or "No active rollouts — verify none pending">
2. <Next rollout action>
3. <Time-sensitive item or "No time-sensitive items">
4. Check Dev merge queue health (CI Health Dashboard)
5. Check monitoring alerts
6. <Verify pipeline/automation>
7. <Babysit PR>
8. Routine monitoring: IcM portal sweep, oncall dashboard triage, Prow status
```

#### Step 3: Create Tracking JIRA

Use the `create-jira-issue` skill's conventions (same cloudId, project,
component IDs, sprint lookup). The tracking ticket must have:

- **Summary**: `Oncall shift tracking — <date> <shift_name> shift (<engineer>)`
- **Type**: Task (ID: 10014)
- **Labels**: `oncall`, `no-qe`
- **Components**: `aro-hcp-oncall` (84116) + any relevant area component
  (e.g. `aro-hcp-releases` 83787 if rollouts are primary)
- **Priority**: Major (oncall tracking tickets are important by definition)
- **Sprint**: Current active sprint (query with
  `project = AROSLSRE AND sprint in openSprints()`)
- **Description**: Full prioritized plan with all sections:
  - Overview (shift context)
  - Prioritized Task List (with all categories)
  - Acceptance Criteria
  - Definition of Done
  - Evidence (links to rollouts, PRs, portals)
  - Handover Chat Excerpts (with attribution)
  - References

Transition to **In Progress** (transition ID: 51).

### Phase 2 — Track Updates During Shift

When the user reports progress, new issues, or status changes:

#### Adding Updates

Post JIRA comments using `mcp_jira_addCommentToJiraIssue` with
`contentFormat: "markdown"`. Each update comment should include:

```markdown
## Shift Update — <HH:MM>

### <Topic>

**Status**: <new status>
**Action taken**: <what was done>
**Result**: <outcome>

### Chat Excerpt (if applicable)

- **<Person> [<time>]**: <message> ([Slack](<link>))
```

#### Creating Sub-tickets

If a new issue is discovered during the shift that warrants its own ticket:

1. Use the `create-jira-issue` skill to create it
2. **Always add the `oncall` label**
3. Link it to the shift tracking ticket using `mcp_jira_createIssueLink`
   (link type: "Relates")
4. Add a comment to the tracking JIRA noting the new ticket

#### Tracking Chat Excerpts

When the user pastes a Slack conversation or references one:

1. Extract key statements with attribution
2. If no Slack link is provided, ask: *"Can you share the Slack link for
   this conversation so it's traceable in the handover?"*
3. Record in the JIRA comment with the standard format

### Phase 3 — Generate Handover Notes

When the user asks for handover notes (end of shift):

#### Step 1: Gather State

Review the tracking JIRA and all comments to compile current state of
every tracked item.

#### Step 2: Generate Handover

Use this exact template:

```markdown
## ARO HCP SL Handover <FROM_SHIFT> to <TO_SHIFT> — <DATE>

### Highlights

<Things worth sharing with other shifts — context, learnings, process
changes, awareness items. Things that happened or were discovered during
the shift that the next shift should know about even if no action is needed.>

*Leave blank if nothing noteworthy.*

### High Priority Tasks

| Item | Status | Action Needed | Links |
|------|--------|---------------|-------|
| <item> | <status> | <what next shift should do> | JIRA / PR / Slack |

*Include JIRA tickets, PRs to merge, Slack threads to follow.*
*Leave blank if nothing pending.*

### High Priority IcM Incidents

| Incident | Severity | Status | Action Needed |
|----------|----------|--------|---------------|
| <title> | <sev> | <status> | <next step> |

*Leave blank if no active incidents.*

### Low Priority Stuff

<Things that are not critical but would be nice to review if the next
shift has spare time. Monitoring items, nice-to-have PRs, cleanup tasks.>

*Leave blank if nothing.*

### Tracking

- **Shift JIRA**: <AROSLSRE-XXXX>
- **Oncall engineer**: <name>
- **Shift**: <shift_name> (<start_time> — <end_time>)
```

#### Step 3: Post to JIRA

Add the handover notes as a final comment on the tracking JIRA.

If the shift is fully complete (all items resolved or handed over):
- Transition to **Review** (transition ID: 61)

If items are still active:
- Keep in **In Progress** and note in the handover that the next shift
  should continue tracking on the same ticket (or create a new one).

### Mandatory Handover Process

The out-going IC is **fully accountable** for ensuring a successful transfer.
The shift is not considered complete until all open issues are documented
and the handover has been proactively sought and confirmed.

#### 15 Minutes Before Shift End

1. **Proactively contact the in-coming IC** via the automatically-created
   Slack handover thread. Tag the incoming IC and confirm they are alert
   and ready.
2. **Complete the handover message** in the Slack thread using the handover
   template above.
3. For every unresolved issue, provide:
   - **JIRA link** with status and next steps clearly updated
   - **PR links** (if applicable)
   - **Slack thread links** to relevant conversations
   - **Current status and next steps** — a clear statement of what the
     incoming IC needs to do

#### Handover Meeting (If Needed)

If the Slack message is insufficient for the complexity of open issues:

1. Initiate and lead the handover meeting using the scheduled meeting link.
2. Ensure the incoming IC attends.
3. Document key decisions and action items in the relevant JIRA tickets.

#### Accountability

Failure to perform a complete and proactive handover does not guarantee a
follow-up from the incoming GEO zone. The out-going IC owns the handover.

## IcM Alert Triage

IcM is the primary tool for engaging SREs to resolve service interruptions.
Access the portal at https://portal.microsofticm.com/ (requires a properly
set up VM or SAW device).

### Acknowledgement SLA

**All Sev 2 and below critical alerts must be acknowledged within 30 minutes.**
This is the escalation policy window — missing it triggers automatic
escalation.

### Triage Process

For every IcM alert:

1. **Acknowledge** the alert within the 30-minute window.
2. **Initiate investigation** — review the alert details, check related
   dashboards and logs.
3. **Create a JIRA ticket** with the `oncall` label documenting the issue,
   current status, and next steps.
4. **Route the issue**:
   - **Component issue** → assign to the component owner's team.
   - **Infrastructure issue** → work on resolution directly.
5. **Document resolution** — update the JIRA ticket with the outcome.
   If the issue is not resolved by end of shift, include it in handover
   notes with full context.

### During Routine Monitoring

Even without active alerts, sweep the IcM portal once per shift to check
for:

- New alerts that may not have paged
- Alerts assigned to the team that need attention
- Stale alerts that should be resolved or re-assigned

## Dev Merge Queue Monitoring

Monitoring the Dev e2e-parallel merge queue is a core oncall responsibility.
The goal is to keep the merge queue moving by unblocking failing Tide
batches.

**CI Health Dashboard**: https://cihealth.tools.hcpsvc.osadev.cloud/

All monitoring should be done via the CI Health Dashboard:

### Day-to-day workflow

- **Report page** (`/report`, DEV env) — watch the E2E success card (after
  the last push of a merged PR). This is the key metric. A dropping value
  is your alarm.
- **Run Log** (`/run-log?date=<YYYY-MM-DD>&env=dev`) — type `batch` in the
  search box (or use `&q=batch`) to filter for Tide batch runs. A cluster
  of failing batches means the queue is blocked. Each row links to the Prow
  job and shows the matched failure.
- **Failure Patterns** (`/failure-patterns`) — this is a triage tool, not a
  monitoring tool. Use it to rank the most impactful issues by run-impact %.
  Category signals (Regression vs. Flake, provision & e2e only) help
  separate a new PR-induced break from a long-standing flake.

### Triage guidance

- **Flake vs. regression**: Tide merges PRs in batches; every PR in a batch
  has already passed e2e on its own. A failing Tide batch is either a flake
  or a bad interaction between batched PRs. Don't chase every red run.
- **Timeout patterns**: Patterns like `CreateHCPClusterAndWait`,
  `context deadline exceeded`, etc. are generic timeouts hiding distinct
  root causes. Occurrence count alone is meaningless. Timeouts require
  deeper analysis from Kusto logs using `hcpctl snapshot analyze` (in
  `tooling/hcpctl`) against the run's snapshot.

## Escalation

When the IC cannot resolve an issue, or the issue belongs to a component
outside Service Lifecycle's responsibility:

1. **Identify the owning team** using the
   [Service Component Escalation Paths](https://docs.google.com/document/d/1fqH__2cv0GU4oiUYAnhl08x61b7CuDbVi3OkC-J58LA/edit?tab=t.0)
   (maintained externally).
2. **Follow the prescribed channel** — open the appropriate tickets and/or
   engage via the documented Slack channels for that component.
3. **Document the escalation** in the shift JIRA with: who was contacted,
   when, via what channel, and the current status.

## Swarm

A Swarm should be initiated when the IC is overwhelmed by a high volume of
complex interruptions (rollouts, critical alerts, major incidents) that make
completing the shift unsustainable.

### How to Initiate

1. Use the `/Swarm` command in `#hcm-aro-team-service-lifecycle`.
2. Select the service **"ARO SLC Swarm"**.
3. Specify:
   - The **region (GEO)** from which assistance is required.
   - The **number of associates** needed.

### Expectations

Team members summoned by a Swarm are expected to:

- Respond promptly
- Assess the required assistance
- Assume a portion of the on-call workload from the IC

## Chat Excerpt Rules

These rules are absolute. Oncall decisions often trace back to Slack
conversations, and without proper attribution the context is lost.

1. **Always record who said it** — use their Slack display name.
2. **Always record when** — timestamp from Slack (format: `[HH:MM AM/PM]`).
3. **Always include the Slack link** — if not provided by the user, ask for
   it explicitly: *"What's the Slack link for that message?"*
4. **Preserve the original wording** for critical decisions — don't
   paraphrase instructions like "cancel the rollout" or "get approval from X".
5. **Flag missing attribution** — if you can't determine who said something
   or when, record it with `*(attribution unclear — verify)*`.

## JIRA Label Enforcement

Every JIRA ticket created or updated during an oncall shift via this skill
**must** have the `oncall` label. This includes:

- The shift tracking ticket itself
- Any sub-tickets created for issues found during the shift
- Any existing tickets that are updated as part of oncall work

When updating an existing ticket that doesn't have the `oncall` label, add
it via `mcp_jira_editJiraIssue` with
`fields: {"labels": [<existing_labels>, "oncall"]}`.

## Associate Obligations

By participating in the oncall rotation, associates agree to:

- **Acknowledgement SLA**: Always acknowledge alerts within the escalation
  policy window (typically 10–15 minutes for pages, 30 minutes for IcM).
- **Fitness for duty**: Associates must not be impaired by alcohol or
  medication that affects their ability to perform technical tasks.
- **Replacement responsibility**: If unable to fulfill duties, source a
  replacement and put in an override into IcM. If unable to find a
  replacement (or in an emergency/illness), inform your manager and get
  confirmation.
- **Comp day**: Working a Recharge Day or Company-Funded Day entitles the
  associate to one Comp Day, which must be taken within two weeks.

## Reference: MCP Tools Used

| Tool | Purpose |
|------|---------|
| `mcp_jira_createJiraIssue` | Create shift tracking ticket or sub-tickets |
| `mcp_jira_addCommentToJiraIssue` | Post updates and handover notes |
| `mcp_jira_transitionJiraIssue` | Change ticket status |
| `mcp_jira_editJiraIssue` | Add labels, update fields |
| `mcp_jira_searchJiraIssuesUsingJql` | Find current sprint, related tickets |
| `mcp_jira_createIssueLink` | Link sub-tickets to tracking ticket |
| `mcp_jira_getJiraIssue` | Read current ticket state |

## Reference: Component IDs

| Component | ID |
|-----------|----|
| aro-hcp-oncall | 84116 |
| aro-hcp-releases | 83787 |
| aro-hcp-e2e | 83786 |
| aro-hcp-ci | 32027 |
| aro-hcp-infra | 84112 |
| aro-hcp-observability | 84111 |

## Reference: Transition IDs

| Transition | ID |
|------------|----|
| In Progress | 51 |
| To Do | 41 |
| Review | 61 |
| Closed | 81 |
| Backlog | 21 |
