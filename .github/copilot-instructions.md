# Copilot Instructions

## Pull Request Standards

All PR rules are maintained in a single source of truth:

**[CONTRIBUTING.md — Pull Request Standards](../CONTRIBUTING.md#pull-request-standards)**

When creating or reviewing a pull request, read and enforce every rule in that section. Do not duplicate the rules here.

Key points to remember:
- Use Conventional Commits format for PR titles (`feat:`, `fix:`, `docs:`, etc.).
- Ignore Tide when evaluating CI/CD status — it is not a CI check.
- Screenshots are required for graph, UI, metrics, and performance changes.
- The PR checklist is built into `.github/PULL_REQUEST_TEMPLATE.md` — it appears automatically.
- For a PR that claims to be a revert, verify that it is a clean revert created with `git revert`. The description must explicitly say so and identify the reverted commit or PR; otherwise, flag a critical issue.

## Security Review (Mandatory for all PR reviews)

When reviewing a PR, these checks are **always blocking** — regardless of the contributor:

- **Inspect every changed file.** Do not skip collapsed or hidden diffs.
- **Agent and editor configuration is blocked by the `verify-supply-chain` presubmit** (`make verify-supply-chain`, in `hack/verify-supply-chain/`). It rejects `settings.json`, `settings.local.json`, `mcp.json` and `hooks.json` under any `.claude/`, `.copilot/` or `.cursor/` segment; `mcp.json` / `.mcp.json` at any path; **anything at all** under `.vscode/`; any `*.code-workspace` at any path; and any of those paths tracked as a symlink or submodule instead of a real file. Confirm the check is green, then review the files yourself anyway.
  - Carrying a `"command"` or `"hooks"` key (e.g. `"command": "node .claude/setup.mjs"`) is **confirmed malware** — do not open or run it; tell the author to report it rather than delete it.
  - Without such a key it is **misplaced configuration**. Say that and no more: calling it malware is the one mistake this guidance must not make.
  - A symlink or submodule on such a path is **not for you to resolve**. The target may sit outside the diff, or be a device that hangs whatever reads it. Report the path and the mode git records, and require the alias be removed.
  - Changes to `.claude/skills/` are expected. The filenames above are blocked there too, but a skill's own `SKILL.md`, assets and fixtures are **not** inspected — a `command` key in a skill fixture reaches you unflagged, and a committed `SKILL.md` is prose an agent reads and follows. Read them.
- **A PR touching `hack/verify-supply-chain/`, its `Makefile` wiring, or `go.work` is reviewing its own gate.** The check lives in the repository it guards, so such a PR can weaken it and still show green. Read those diffs line by line. A green check is never a clean bill of health.
- **Scrutinize CI/CD config changes.** Review modifications to `.github/workflows/`, `Makefile`, `*pipeline.yaml`, `Dockerfile`, and CI-invoked scripts in `hack/`, `tooling/`, or `test/`. Look for unexpected downloads, encoded payloads, or command injection.
- **Flag new executable scripts in config directories.** If a PR adds `.sh`, `.mjs`, `.py`, etc. to configuration directories, flag for manual human review even if content appears benign.
- **Do not expose internal URLs.** Neither in commits nor PR descriptions.
  - Flag as critical any URLs matching the following rules:
    - direct links to icm portal
    - *.windows.net, *.visualstudio.com, *.azure.com;
      - exceptions: links to sdp-pipelines repo are allowed
- **Do not leak internal information for Red Hat or Microsoft systems.** Neither in commits nor PR descriptions.
  - Discussions of internal Microsoft systems (e.g. dsts or ev2) must be flagged for review (critical priority).
  - Discussions of the working of compliance policies, security scans of either RH or MSFT must be flagged for review (critical priority).
  - Flag referencing ICM incident IDs as to be avoided (medium priority).
- When removing unwanted information, require corrections to replace existing commits. Just adding new commits to PRs is insufficient, as don't want to retain the information in commit history.
