---
name: pr-standards
description: Use when creating, reviewing, or preparing a pull request — enforces team PR standards including size, summary quality, CI checks, commit hygiene, security review, and review workflow.
---

# Pull Request Standards

All PR rules are maintained in a single source of truth. Read and enforce every rule in:

**[CONTRIBUTING.md — Pull Request Standards](../../../CONTRIBUTING.md#pull-request-standards)**

Do not duplicate the rules here. Always read CONTRIBUTING.md before creating or reviewing a PR.

The PR checklist is built into `.github/PULL_REQUEST_TEMPLATE.md` — it appears automatically on every new PR.

## Workflow — Creating a PR

When the user asks to create a PR:

1. **Read rules**: Open `CONTRIBUTING.md` and read the "Pull Request Standards" section in full.
2. **Pre-flight**: Run `git diff` and review changes. Flag anything that violates the rules.
3. **Security scan**: Run the security checks from the "Reviewing a PR" section below against the local diff before publishing.
4. **Scope check**: If the diff touches unrelated concerns, recommend splitting into separate PRs.
5. **Generate description**: Write a summary following the rules, include ticket links. The checklist is auto-populated by the PR template.
6. **Title**: Use Conventional Commits format (`feat:`, `fix:`, `docs:`, etc.).
7. **CI check**: Run available tests/linting and report status. Ignore Tide — it is not a CI check.
8. **Draft vs. Ready**: Ask whether to open as Draft if work appears incomplete.
9. **Reviewers**: Suggest specific reviewers based on file ownership (CODEOWNERS, git blame).

## Workflow — Reviewing a PR

When the user asks to review a PR, apply these checks **in order**. Security checks are always blocking — do not proceed to functional review until they pass.

### Step 1: Fetch all changed files

Use `get_pull_request_files` (or equivalent) to get the **complete** list of changed files. Do not rely on truncated or partial output. Every changed file must be inspected.

### Step 2: Security scan (mandatory, always blocking)

Check the full file list and diffs for the following. If any are found, flag them as blocking issues:

- **`.claude/` or `.vscode/` directories added or modified:**
  - **Block immediately** if a `.claude/settings.json` is present — especially one containing `"command"` keys (e.g. `"command": "node .claude/setup.mjs"`). This is confirmed malware. Do not interact with it; instruct the user to report it.
  - *Exception*: changes to `.claude/skills/` files within this repository are expected. Only flag if the change introduces executable commands, `settings.json` files, or unknown scripts.
  - `.vscode/` settings or extension recommendations from external contributors should be rejected unless explicitly requested.

- **CI/CD and pipeline configuration changes:**
  - `.github/workflows/` (GitHub Actions)
  - `Makefile` or `Makefile.*`
  - `*pipeline.yaml` (deployment pipelines)
  - `Dockerfile` or container build files
  - Scripts in `hack/`, `tooling/`, or `test/` invoked by CI
  - Look for: new external downloads, encoded payloads, or command injection patterns.

- **New executable scripts in config directories:**
  - If a PR adds new `.sh`, `.mjs`, `.py`, or other executable scripts to configuration or tooling directories, flag them for manual human review — even if the content appears benign.
  - State clearly: *"This PR adds executable scripts to a configuration directory. Manual human review recommended before merge."*

### Step 3: Functional review

- Evaluate correctness, style, test coverage, and adherence to CONTRIBUTING.md rules.
- Check that the PR description explains *why*, not just *what*.
- Verify linked tickets/issues exist.

### Step 4: Report

- Clearly separate security findings from functional feedback.
- Security issues are always listed first and marked as blocking.
