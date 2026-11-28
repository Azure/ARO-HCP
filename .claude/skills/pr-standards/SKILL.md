---
name: pr-standards
description: Use when creating, reviewing, or preparing a pull request — enforces team PR standards including size, summary quality, CI checks, commit hygiene, security review, review workflow, fresh-context review, unit test value, and E2E evidence.
---

# Pull Request Standards

All PR rules are maintained in a single source of truth. Read and enforce every rule in:

**[CONTRIBUTING.md — Pull Request Standards](../../../CONTRIBUTING.md#pull-request-standards)**

Do not duplicate the rules here. Always read CONTRIBUTING.md before creating or reviewing a PR.

The PR checklist is built into `.github/PULL_REQUEST_TEMPLATE.md` — it appears automatically on every new PR.

Automated PR policy is maintained in
[CONTRIBUTING.md — Pull Request Standards](../../../CONTRIBUTING.md#pull-request-standards).
Follow it without duplicating it here.

Reviewers are the bottleneck. The workflow below adds agent-specific rules that save their time.

## Model bar

Author PRs only with Sol 6+ or Claude Opus 5.5+. One-liners are exempt. If you are below the bar, say so and stop before drafting.

## Workflow — Creating a PR

When the user asks to create a PR:

1. **Read rules**: Open `CONTRIBUTING.md` and read the "Pull Request Standards" section in full.
2. **Pre-flight**: Review the complete diff against the PR base, including uncommitted changes (`git diff "$(git merge-base <base> HEAD)"`), plus any untracked files meant for the PR. Flag anything that violates the rules. Repeat self-review until a pass finds nothing, and count the passes.
3. **Security scan**: Run the security checks from the "Reviewing a PR" section below against the local diff before publishing.
4. **Scope check**: If the diff touches unrelated concerns, recommend splitting into separate PRs.
5. **Trim**: Rate every new or changed unit test and drop the lows (see [Unit test value](#unit-test-value)). Drop README or docs additions that only describe implementation detail (see [README and docs](#readme-and-docs)).
6. **Independent review**: Get a fresh-context review (see [Independent review](#independent-review)). Address every finding or reject it with a one-line reason. After material fixes, review again.
7. **Test for real**: Unit tests and lint are table stakes. What gets a PR merged is a real end-to-end run that passed against the final diff and exercises the changed behavior, such as the relevant [E2E tests](../../../test/e2e/README.md). For instruction-only changes such as skills, run the changed workflow in a fresh session. Rerun after material fixes. If you truly cannot run it, state exactly what blocked it. Never imply a test ran when it did not.
8. **Generate description**: Follow the evergreen-description policy in `CONTRIBUTING.md` and the [Description](#description) rules below; include ticket links, or the documented no-ticket explanation when no ticket exists. The checklist is auto-populated by the PR template.
9. **Title**: Use Conventional Commits format (`feat:`, `fix:`, `docs:`, etc.).
10. **CI check**: Run available tests/linting and report status. Ignore Tide — it is not a CI check.
11. **Create Draft PR**: Follow the automated PR policy in `CONTRIBUTING.md`.
12. **Reviewers**: Suggest specific reviewers based on file ownership (CODEOWNERS, git blame).

### Description

Fill the PR template's **What**, **Why**, and **Testing** sections in a few short paragraphs total. An AI wall of text goes to the bottom of the review pile.

- **Testing** states what actually ran and the result, end-to-end first, then unit and lint. If no tests were added, keep the template's justification.
- Close with one line: "Self-reviewed in N iterations." Do not describe what changed between iterations.
- Put optional material (tradeoffs, follow-ups, non-goals, flaky checks) in **Special notes for your reviewer**. Delete that section if it is empty.
- Do not include a file-by-file restatement of the diff.

### Independent review

Get at least one review from a fresh subagent or separate session with no author context. That is the point of the review. Give the reviewer only the branch or worktree path and the PR base, the ticket or problem statement, and this skill. Do not pass your reasoning, plan, or conversation. Ask it to run the "Reviewing a PR" workflow below.

- **Default**: one Sol 6.2 (high) reviewer.
- **Large or critical changes**: a council of Sol 6.2 (high), Opus 5.5 (medium), and Gemini 4 Neon (high), run in parallel. Weigh findings that agree more heavily.

### Unit test value

Rate every new or changed test:

- **High**: protects behavior that is easy to break and costly when broken, such as a regression test for the bug being fixed, tricky parsing, or edge cases.
- **Medium**: covers a meaningful branch of new logic that E2E will not reach cheaply.
- **Low**: tests the language or framework, mocks everything, restates the implementation, duplicates another test, or checks trivial getters or wiring.

Ship high and medium. Drop low.

### README and docs

Agent-written README paragraphs usually describe implementation detail. Keep a docs change only if it changes what a user or operator must know or do, such as a new command, flag, setup step, or behavior. Otherwise drop it from the PR.

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
  - Look for new external downloads, encoded payloads, or command injection patterns, particularly in the following CI/CD and pipeline-related paths or files:
    - `.github/workflows/` (GitHub Actions)
    - `Makefile` or `Makefile.*`
    - `*pipeline.yaml` (deployment pipelines)
    - `Dockerfile` or container build files
    - Scripts in `hack/`, `tooling/`, or `test/` invoked by CI

- **New executable scripts in config directories:**
  - If a PR adds new `.sh`, `.mjs`, `.py`, or other executable scripts to configuration or tooling directories, flag them for manual human review — even if the content appears benign.
  - State clearly: *"This PR adds executable scripts to a configuration directory. Manual human review recommended before merge."*

### Step 3: Functional review

- Evaluate correctness, style, test coverage, and adherence to CONTRIBUTING.md rules.
- Check what would regress, and whether a simpler or more conventional approach exists in the repo.
- Rate each new or changed unit test high, medium, or low (see [Unit test value](#unit-test-value)) and list the lows to drop.
- List README or docs additions that are implementation detail and should be dropped.
- Check whether the PR reports a real end-to-end run that passed. Say so if it does not.
- Check that the PR description explains *why*, not just *what*, and fits in a few short paragraphs. Flag walls of text.
- Verify linked tickets/issues exist.

### Step 4: Report

- Clearly separate security findings from functional feedback.
- Security issues are always listed first and marked as blocking.
