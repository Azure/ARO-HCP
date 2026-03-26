# Severity and Confidence

## Severity

- **blocker** — likely merge blocker or immediate rollout risk: broken contract, unsafe migration, missing generated output, clear security/RBAC issue, dangerous broad retry behavior.
- **high** — important correctness or safety issue that should be fixed before merge unless disproven by evidence.
- **medium** — plausible issue or missing evidence that weakens confidence but may have a safe explanation.
- **low** — minor follow-up or cautionary note; avoid using these unless they add real operational value.

## Confidence

- **high** — directly supported by code, tests, generated drift, or an explicit repo invariant.
- **medium** — likely issue, but based on indirect evidence or incomplete review context.
- **low** — possible concern only; usually escalate or suppress rather than presenting as a hard finding.

## Policy

- Prefer fewer high-confidence findings over many speculative ones.
- Low-confidence findings should normally become an escalation or explicit question, not a definitive failure claim.
