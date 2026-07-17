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
   3. Check alerts (proactive problem detection)
   4. Routine monitoring (IcM, Prow, dashboards, quality call)

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

Apply the fixed priority order:

1. **🔴 Rollouts** — Active rollouts, failed rollouts needing cancellation,
   blocked rollouts.
2. **🟠 Important / Time-Sensitive** — CCOA exceptions, approval chains,
   PRs with deadlines, pipeline recoveries.
3. **🟡 Alerts** — Check monitoring alerts that might catch problems early.
4. **🟢 Awareness / Routine** — Prow status, IcM portal, quality call,
   handover dashboard, standard monitoring.

Present as a numbered action sequence (not just categories):

```
### Suggested Sequence

1. <Most urgent rollout action>
2. <Next rollout action>
3. <Time-sensitive item>
4. <Check alerts>
5. <Verify pipeline/automation>
6. <Babysit PR>
7. Routine monitoring (Prow, IcM, dashboard)
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
