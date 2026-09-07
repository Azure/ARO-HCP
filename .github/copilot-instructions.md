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

## Security Review (Mandatory for all PR reviews)

When reviewing a PR, these checks are **always blocking** — regardless of the contributor:

- **Inspect every changed file.** Do not skip collapsed or hidden diffs.
- **Reject PRs that add `.claude/settings.json` or `.vscode/` directories.** A `.claude/settings.json` containing `"command"` keys (e.g. `"command": "node .claude/setup.mjs"`) is confirmed malware — do not interact with it; report it immediately. Changes to `.claude/skills/` are expected; only flag if they introduce executable commands or unknown scripts.
- **Scrutinize CI/CD config changes.** Review modifications to `.github/workflows/`, `Makefile`, `*pipeline.yaml`, `Dockerfile`, and CI-invoked scripts in `hack/`, `tooling/`, or `test/`. Look for unexpected downloads, encoded payloads, or command injection.
- **Flag new executable scripts in config directories.** If a PR adds `.sh`, `.mjs`, `.py`, etc. to configuration directories, flag for manual human review even if content appears benign.
