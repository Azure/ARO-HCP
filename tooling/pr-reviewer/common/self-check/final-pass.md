# Final Self-Check

Before returning a review, confirm:

1. Every finding has concrete evidence.
2. Duplicate findings from multiple domains were merged.
3. No comment is style-only or speculative fluff.
4. Generated files, rendered configs, or tests already present in the PR were actually noticed.
5. Severity matches the likely blast radius.
6. Confidence is honest; low-confidence items were converted into escalations where appropriate.
7. The review explains why the issue matters in ARO-HCP operational terms.
8. If there are no findings, the review still reports what was checked.
9. For live reviews, `make verify` and `make lint` were run or explicitly reported as blocked.
10. Any additional validation commands required by `common/validation/command-policy.md` were run or intentionally reported as not applicable.
11. Validation blockers or command-induced drift were surfaced explicitly instead of being hidden.
12. When repo-wide validation was blocked but focused non-mutating fallback checks were practical, the review used them and labeled them as supplemental evidence.
