# Security / Privacy Guardrails for History Mining

- Store only review artifacts relevant to ARO-HCP engineering decisions.
- Do not store secrets, tokens, or copied CI logs that contain sensitive values.
- Prefer summaries or structured metadata over dumping large raw logs.
- If a comment contains sensitive operational detail, distill the lesson instead of preserving the full text.
- Keep fixtures focused on review rationale, changed paths, and outcome.
