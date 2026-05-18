# Contributing to ARO HCP

Welcome to the ARO HCP project! We appreciate your interest in contributing. This guide will help you get started with the contribution process.


## Table of Contents
- [Getting Started](#getting-started)
- [Contributing Guidelines](#contributing-guidelines)
- [Pull Request Standards](#pull-request-standards)
- [AI Skills](#ai-skills)
- [PR and Issue Lifecycle](#pr-and-issue-lifecycle)
- [Code of Conduct](#code-of-conduct)
- [License](#license)


## Getting Started

To contribute to ARO HCP, follow these steps:

1. Fork the [ARO-HCP repository](https://github.com/Azure/ARO-HCP) using the **Fork** button on GitHub.
2. Clone your fork to your local machine:
   ```sh
   git clone https://github.com/<your-username>/ARO-HCP.git
   cd ARO-HCP
   ```
3. Add the upstream repository as a remote so you can keep your fork up to date:
   ```sh
   git remote add upstream https://github.com/Azure/ARO-HCP.git
   ```
4. Create a new branch for your changes:
   ```sh
   git fetch upstream
   git checkout -b my-feature upstream/main
   ```
5. Make your changes and commit them.
6. Push your branch to your fork:
   ```sh
   git push origin my-feature
   ```
7. Open a pull request from your fork's branch against `main` in the upstream ARO-HCP repository.

### Keeping your fork up to date

Before starting new work, sync your fork with upstream:
```sh
git fetch upstream
git checkout main
git merge upstream/main
git push origin main
```

## Contributing Guidelines
Please follow these guidelines when contributing to ARO HCP:

- The repository is structured according to the focus areas, e.g. `api` containing all exposed API specs.
  When you contribute, please follow this structure and add your contribution to the appropriate folder.
  When in doubt, open PR early and ask for feedback.
- When applicable, please always cover new functionality with the appropriate tests.
- When adding functionality, that is not yet implemented, please write appropriate documentation.
  When in doubt, ask yourself what it took you to understand the functionality, and what would you need
  to know to use it.
- When adding new features, please consider to record a short video showing how it works and explaining
  the use case. This will help others to understand better even before digging into the code. When done,
  upload the recording to the [Drive](https://drive.google.com/drive/folders/1RB1L2-nGMXwsOAOYC-VGGbB0yD3Ae-rD?usp=drive_link) and share the link in the PR.
- When you are working on the issue that has Jira card, please always document all tradeoffs and decisions
  in the Jira card. Please, also include all design documents and slack discussion in the Jira. This will
  help others to understand the context and decisions made.

Please note, that you might be asked to comply with these guidelines before your PR is accepted.

## Pull Request Standards

All pull requests must follow these standards. Reviewers will check for compliance before approving.

### 1. Keep PRs Small
- One PR per task — do not mix features with refactors, style fixes, or unrelated bug fixes.
- If a task requires multiple concerns, split them into separate PRs.

### 2. Use Informative PR Titles
- Titles should be clear, concise, and follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format where applicable.
- Use a type prefix: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, etc.
- Example: `feat: add MISE v2 header-based routing` — not `update routing`.
- The title becomes the squash-merge commit message, so make it meaningful for `git log`.

### 3. Write a Clear Summary
- The PR description must explain the **"Why"** behind the change, not just the "What".
- Example: _"Increased node pool max count from 5 → 10 to handle Black Friday traffic spikes"_ — not _"Changed max count"_.
- Lead with motivation and business context, then briefly describe the technical approach.

### 4. Link to Relevant Ticket or Issue
- Every PR must reference its tracking ticket (Jira, GitHub Issue).
- Use formats like `Fixes #123`, `Closes PROJ-456`, or a direct URL.
- If no ticket exists, create one first or explain why in the PR body.

### 5. Include Screenshots for Graph/UI/Metrics/Performance Changes
- Any change that affects dashboards, graphs, metrics visualizations, UI, or performance/metrics must include before/after screenshots.
- This includes changes to alerting rules, SLOs, monitoring dashboards, and any observable behavioral change.
- Annotate screenshots when the change is subtle.

### 6. Self-Review Before Requesting Review
- Run `git diff` and review every changed line yourself before requesting others.
- Look for: leftover debug code, TODOs, unintended changes, secrets, formatting issues.

### 7. CI/CD Checks Must Pass
- All tests, linting, and CI/CD pipeline checks must be green before requesting review, **excluding Tide**.
- Tide is a merge-automation bot and its status is not a CI/CD check — do not wait on it or treat it as a blocker.
- If a non-Tide check is flaky or unrelated, note it explicitly in the PR description — do not ignore it silently.

### 8. Use Draft PRs for WIP
- If requesting early feedback or the work is incomplete, open the PR as a **Draft**.
- Convert to "Ready for Review" only when all checks pass and self-review is done.

### 9. Keep Commit History Clean
- Use interactive rebase (`git rebase -i`) to squash or fixup commits into a clean, logical history.
- Each commit message should be meaningful — no "fix typo" or "wip" commits in the final history.
- The PR will be squashed before merging, unless splitting into multiple commits is explicitly
  needed to separate changes and allow later `git bisect`.

### 10. Comment on Tricky Code
- Add inline comments on any non-obvious logic explaining **why** a particular approach was chosen.
- Flag performance tradeoffs, workarounds, or temporary solutions.

### 11. Tag Specific Reviewers
- Tag **specific owners or subject-matter experts** who are most relevant to the changed code.
- For cross-team changes, tag reviewers from each affected team.

### 12. Resolve All Comment Threads
- After addressing review feedback, resolve each comment thread.
- If you disagree with feedback, reply with your reasoning before resolving — do not silently dismiss.
- A PR should have zero open threads before merging.


## AI Skills

This repository includes AI agent skills — structured instructions that coding agents follow for specific workflows like creating or reviewing PRs.

Skills live in `.claude/skills/` as `SKILL.md` files with YAML frontmatter. Claude Code, Crush, and Cursor all auto-load skills from this directory — no configuration needed. For GitHub Copilot, a condensed version lives in `.github/copilot-instructions.md`.

### Adding New Skills

1. Create a directory under `.claude/skills/<skill-name>/`.
2. Add a `SKILL.md` with YAML frontmatter (`name`, `description`) and the full instruction body.
3. For Copilot compatibility, add the rules to `.github/copilot-instructions.md` as a new section.


## PR and Issue Lifecycle

This repository uses automated Prow lifecycle management to keep PRs and issues from going stale. Inactive PRs and issues progress through three stages before being automatically closed.

### Lifecycle Stages

| Stage | Label | Trigger | What happens |
|-------|-------|---------|--------------|
| **Stale** | `lifecycle/stale` | 90 days of inactivity | A comment is added warning that the PR/issue will be closed if no activity occurs |
| **Rotten** | `lifecycle/rotten` | Stale + continued inactivity | A second warning is added |
| **Closed** | — | Rotten + continued inactivity | The PR/issue is automatically closed |

Any activity (comments, pushes, label changes) resets the inactivity timer and removes the lifecycle label.

### Prow Commands

Use these commands in a PR or issue comment to manage the lifecycle:

| Command | Effect |
|---------|--------|
| `/remove-lifecycle stale` | Remove the stale label and reset the timer |
| `/remove-lifecycle rotten` | Remove the rotten label and reset the timer |
| `/lifecycle frozen` | Exempt the PR/issue from automatic closure entirely |
| `/remove-lifecycle frozen` | Remove the frozen exemption |

### Keeping Long-Running PRs Open

If you have a PR that is intentionally paused (e.g. waiting on a dependency, blocked by another team, or a long-term draft), add the `/lifecycle frozen` command to prevent it from being automatically closed.


## Code of Conduct
This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.


## License
ARO HCP is licensed under the Apache License, Version 2.0. Please see the [LICENSE](LICENSE) file for more details.
