# Domain Memo Output Contract

Fresh-session domain agents return domain memos that the main orchestrator later synthesizes into one TSG draft.

## Template

```markdown
# Domain memo: `<domain name>`

## Incident envelope
- Primary symptom: `<short summary>`
- Time window: `<window or Not yet determined>`
- Cluster scope: `<cluster/HCP or Not yet determined>`
- Join keys: `<stable ids or Not yet determined>`

## Domain relevance
- Why this domain matched: `<routing reason>`

## Evidence ladder
### Tier 1: Incident runtime evidence
- `<best runtime signal>`

### Tier 2: Corroborating evidence
- `<second signal or Not yet determined>`

### Tier 3: Implementation evidence
- `<repo file or TSG that explains the signal>`

## Conclusion
- Dominant lane: `<monitoring-probe | control-plane | inconclusive>`
- Confidence: `<high | medium | low>`
- Leading explanation: `<best current explanation>`
- Smallest next evidence step: `<next check>`
- Escalate when: `<threshold or Not yet determined>`
```

## Output rules

- Return one domain memo and nothing else.
- Use `Not yet determined` for unknown fields.
- Do not return a final TSG from a domain agent.
