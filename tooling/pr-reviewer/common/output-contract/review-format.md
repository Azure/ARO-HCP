# Review Output Contract

Use this structure for every substantive review.

## Template

```markdown
# ARO-HCP review
## Overall assessment
- One short paragraph describing the risk profile of the change.

## Validation
- `make verify`: pass | fail | blocked | not applicable
- `make lint`: pass | fail | blocked | not applicable
- Additional commands: list any extra commands run from `common/validation/command-policy.md`, or say `None`.
- Call out any command blocker or command-induced generated drift that materially affects confidence.

## Findings
### Severity: [severity] Confidence: [confidence] Domain: [domain] Short title
- Why it matters: ...
- Evidence: `path[:line-range]`, plus the violated invariant or historical lesson.
- Recommendation: ...
- Similar history: optional `PR #NNNN` reference when useful.

## Coverage
- Domains checked: ...
- Shared invariants checked: ...
- History consulted: ...

## Escalations
- List any owner/domain follow-up needed, or say `None`.
```

## No-findings case

If there are no findings, use `common/baseline/no-findings.md` instead of returning an empty review, and still include the validation results.
