---
applyTo:
  - "tooling/pr-reviewer/**"
  - ".claude/commands/arohcp/**"
  - ".github/copilot-instructions.md"
  - ".github/instructions/arohcp-reviewer.instructions.md"
---

# ARO-HCP Reviewer Packaging Instructions

Treat `tooling/pr-reviewer/SKILL.md`, `tooling/pr-reviewer/MANIFEST.md`, and `tooling/pr-reviewer/common/validation/command-policy.md` as authoritative for reviewer behavior and packaging.

Keep Claude and Copilot entrypoints thin: they should point at the same reviewer source of truth instead of duplicating review logic.

When changing reviewer routing, validation, or entrypoint packaging, update the matching validator or regression coverage so `make -C tooling/pr-reviewer validate` enforces the new behavior.
