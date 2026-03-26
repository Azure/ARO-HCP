# Priority Domains

Default first-review emphasis should go to:

1. **Config / pipelines** — repo-wide rollout safety, rendered config coupling, retry behavior.
2. **Resource Provider / API** — customer-visible behavior, validation, compatibility.
3. **Azure infra / Bicep** — scope placement, RBAC, identities, logging, networking.
4. **Observability / testing / tooling** — evidence quality, generated artifacts, rollout confidence.
5. **Backend / state** — async operations and persistence compatibility.

The remaining domains are still important, but these areas usually produce the highest review return because they combine broad blast radius with recurring change volume.
