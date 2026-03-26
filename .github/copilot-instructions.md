# ARO-HCP Copilot Instructions

When the user asks for a PR review, branch diff review, commit-range review, path-scoped review, or reviewer eval, use the in-repo ARO-HCP reviewer under `tooling/pr-reviewer/` instead of inventing a separate review flow.

Treat these files as authoritative:

- `tooling/pr-reviewer/SKILL.md`
- `tooling/pr-reviewer/MANIFEST.md`
- `tooling/pr-reviewer/common/validation/command-policy.md`

For live local reviews:

- gather changed paths and classify them with `tooling/pr-reviewer/common/tools/classify_paths.py`
- use `tooling/pr-reviewer/common/domain-routing/path-routing.json`
- always load `sub-reviewers/cross-cutting.md` plus every matched domain specialist
- treat router-selected `history_fixtures` as authoritative
- report validation commands as `pass`, `fail`, `blocked`, or `not applicable`
- keep findings high-signal: correctness, rollout safety, tenancy and security boundaries, generated drift, and operational behavior; avoid style-only noise

When editing the reviewer itself (`tooling/pr-reviewer/`, `.claude/commands/arohcp/`, `.github/copilot-instructions.md`, or `.github/instructions/arohcp-reviewer.instructions.md`), run `make -C tooling/pr-reviewer validate`.
