# TSG Output Contract

Base every substantive incident write-up on the shared TSG template at `/home/swiencki/worktree/Azure-Documents-Common/Teams/Azure RedHat OpenShift/doc/hcp/troubleshooting/template.md`.

For the kernel PR, keep the template structure and additionally:

- add `Incident envelope` to `## Metadata`
- label the diagnosis `Leading hypothesis` unless runtime evidence justifies a confirmed root cause
- write `Not yet determined` for unknown fields

## Template

```markdown
# TSG: `<short incident title>`

## Metadata

- Status: `Draft`
- Owner: `ARO-HCP SRE agent (human review required)`
- Last reviewed: `<yyyy-mm-dd>`
- Incident envelope: `<time window>; <cluster scope>; <primary symptom>; <main join keys>`
- Confidence: `<high | medium | low>`

## When to use this TSG

- Use this TSG when `<customer symptom, alert, or error message>`.

## 1. Purpose
## 2. Severity and impact
## 3. Customer-visible symptoms
## 4. Service / system symptoms

### Evidence used
- `<alerts, dashboards, logs, repo files, or fixture references>`

### Limitations / uncertainties
- `<missing evidence or conflicting signal>`

## 5. Identify the problem

### Diagnostic steps
- Goal: `<what each step proves or rules out>`
- Expected result: `<healthy signal>`
- Common failure signals: `<unhealthy signal>`

### Leading hypothesis / confidence
- Leading hypothesis: `<best current explanation>`
- Confidence: `<high | medium | low>`
- What would raise confidence: `<smallest next check>`

## 6. Mitigation steps

### Escalate when
- `<when a human or privileged access is required>`

## 7. Validation and confirmation
## 8. After incident
```
