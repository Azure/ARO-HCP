---
name: pr-standards
description: Use when creating, reviewing, or preparing a pull request — enforces team PR standards including size, summary quality, CI checks, commit hygiene, security review, and review workflow.
---

# Pull Request Standards

All PR rules are maintained in a single source of truth. Read and enforce every rule in:

**[CONTRIBUTING.md — Pull Request Standards](../../../CONTRIBUTING.md#pull-request-standards)**

Do not duplicate the rules here. Always read CONTRIBUTING.md before creating or reviewing a PR.

The PR checklist is built into `.github/PULL_REQUEST_TEMPLATE.md` — it appears automatically on every new PR.

Automated PR policy is maintained in
[CONTRIBUTING.md — Pull Request Standards](../../../CONTRIBUTING.md#pull-request-standards).
Follow it without duplicating it here.

## Workflow — Creating a PR

When the user asks to create a PR:

1. **Read rules**: Open `CONTRIBUTING.md` and read the "Pull Request Standards" section in full.
2. **Pre-flight**: Run `git diff` and review changes. Flag anything that violates the rules.
3. **Security scan**: Run the security checks from the "Reviewing a PR" section below against the local diff before publishing.
4. **Scope check**: If the diff touches unrelated concerns, recommend splitting into separate PRs.
5. **Generate description**: Follow the evergreen-description policy in `CONTRIBUTING.md`; include ticket links, or the documented no-ticket explanation when no ticket exists. The checklist is auto-populated by the PR template.
6. **Title**: Use Conventional Commits format (`feat:`, `fix:`, `docs:`, etc.).
7. **CI check**: Run available tests/linting and report status. Ignore Tide — it is not a CI check.
8. **Create Draft PR**: Follow the automated PR policy in `CONTRIBUTING.md`.
9. **Reviewers**: Suggest specific reviewers based on file ownership (CODEOWNERS, git blame).

## Workflow — Reviewing a PR

When the user asks to review a PR, apply these checks **in order**. Security checks are always blocking — do not proceed to functional review until they pass.

### Step 1: Fetch all changed files

Use `get_pull_request_files` (or equivalent) to get the **complete** list of changed files. Do not rely on truncated or partial output. Every changed file must be inspected.

### Step 2: Security scan (mandatory, always blocking)

Check the full file list and diffs for the following. If any are found, flag them as blocking issues:

The `ci/prow/verify` presubmit runs `make verify-supply-chain` (implemented in `hack/verify-supply-chain/`). It is **default-deny** over four directories, and decides everything from the git index — the path and the file mode. It never reads what is inside a file:

1. **Nothing under `.claude/`, `.copilot/`, `.cursor/` or `.vscode/`, at any depth, may be committed** unless its exact path is on the allowlist in `hack/verify-supply-chain/main.go`. Today that allowlist is two entries, both `SKILL.md`. A filename denylist came first and could not hold: it blocked `.claude/settings.json` while `.claude/skills/x/my-hooks.json` carrying the same payload landed untouched.
2. Any `mcp.json` or `.mcp.json` anywhere, including the project-scoped file at the repository root, which sits outside those four directories.
3. Any `*.code-workspace`, at any path. These carry the whole of `.vscode/` in one document — settings, extension recommendations, launch configurations and tasks — and a task with `runOptions.runOn: "folderOpen"` runs when the workspace is opened. They usually sit at the repository root.
4. Any tracked entry on one of those paths that is not a real file — a symlink or a submodule — including the directory itself, and including a path that *is* allowlisted. `frontend/.claude -> config` makes `frontend/.claude/settings.json` resolve to `frontend/config/settings.json`, which is tracked under an ordinary name no rule objects to; a submodule mounted at the same path hides it further still, since the parent repository holds nothing but a commit ID. Either way git records only the alias and nothing underneath it.

**A PR that adds an allowlist entry is asking you to approve a file, not just a diff.** Read it in full. A `SKILL.md` is prose an agent follows — it needs no executable bit and no script, and no automated rule inspects it. Confirm the file is genuinely needed and cannot execute anything by itself before approving the entry.

**Do not resolve such a finding yourself.** The check declines to follow the alias deliberately, and so should you. The target may sit outside the diff, or be a fifo or device that hangs or leaks whatever reads it; a submodule means fetching a repository the author chose. Report what git records — the path and its mode — and require the alias be removed. Every one of these is already blocking, so there is nothing to establish by looking: working out where it really pointed is for a human in a disposable environment, afterwards.

**It does not read file contents at all.** A path is refused for being unlisted, not for what it holds, so nothing is parsed and no finding claims a file is malware. That cuts both ways: an allowlisted file can contain anything, and a `command` key anywhere outside the four directories reaches you unflagged. The attack this leaves to you runs through the prose of an approved `SKILL.md`, which no automated rule inspects.

**Inspect the changed files yourself regardless.** The check lives in the repository it guards, so the same PR can weaken `hack/verify-supply-chain/` or drop it from `make verify` and still show green. Treat it as a second pair of eyes, never as a reason to skip looking. In particular, a PR that touches the verifier, its Makefile wiring, or `go.work` is reviewing its own gate — read those diffs line by line. And note that a committed `SKILL.md` is itself an attack surface: it is prose an agent reads and follows, needs no executable bit and no script, and no automated rule here will catch it.

- **Agent or editor configuration added or modified:**
  - All of it is blocked by the gate above unless allowlisted, so the question is not whether it may land but whether it should be approved — and that is your judgement, not the gate's. **Do not open or run one the author cannot account for**; a committed agent config executes on clone. Have them report it rather than delete it.
  - A file the author can account for is misplaced configuration, or a candidate for the allowlist. Say that, and no more: calling it malware is the one mistake this guidance must not make, and the check no longer makes that claim for you.
  - *Exception*: changes to `.claude/skills/` files are expected here. Flag one only if it introduces executable commands, a settings filename, or scripts whose purpose is not obvious.
  - `.vscode/` is rejected for every contributor, with no override and no "unless requested" — nothing there is legitimately tracked, and editor settings belong in a local working copy.

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
- Check that the PR description explains *why*, not just *what*.
- Verify linked tickets/issues exist.

### Step 4: Report

- Clearly separate security findings from functional feedback.
- Security issues are always listed first and marked as blocking.
