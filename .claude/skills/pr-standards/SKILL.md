---
name: pr-standards
description: Use when creating, reviewing, or preparing a pull request — enforces team PR standards including size, summary quality, CI checks, commit hygiene, and review workflow.
---

# Pull Request Standards

All PR rules are maintained in a single source of truth. Read and enforce every rule in:

**[CONTRIBUTING.md — Pull Request Standards](../../../CONTRIBUTING.md#pull-request-standards)**

Do not duplicate the rules here. Always read CONTRIBUTING.md before creating or reviewing a PR.

The PR checklist is built into `.github/PULL_REQUEST_TEMPLATE.md` — it appears automatically on every new PR.

## Workflow

When the user asks to create a PR:

1. **Read rules**: Open `CONTRIBUTING.md` and read the "Pull Request Standards" section in full.
2. **Pre-flight**: Run `git diff` and review changes. Flag anything that violates the rules.
3. **Scope check**: If the diff touches unrelated concerns, recommend splitting into separate PRs.
4. **Generate description**: Write a summary following the rules, include ticket links. The checklist is auto-populated by the PR template.
5. **Title**: Use Conventional Commits format (`feat:`, `fix:`, `docs:`, etc.).
6. **CI check**: Run available tests/linting and report status. Ignore Tide — it is not a CI check.
7. **Draft vs. Ready**: Ask whether to open as Draft if work appears incomplete.
8. **Reviewers**: Suggest specific reviewers based on file ownership (CODEOWNERS, git blame).
