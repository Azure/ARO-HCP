# Supply-Chain Presubmit (`make verify-supply-chain`)

`hack/verify-supply-chain/` is a Go tool that inspects every file tracked by git and fails if any of them matches a high-confidence supply-chain attack indicator — a committed AI-agent settings file, MCP server configuration, or editor configuration. It is a dependency of `make verify`, so it runs on every PR through the always-on `ci/prow/verify` presubmit, and it is the first dependency in that list so it fails in under a second rather than after the multi-minute codegen checks.

This page documents the threat model, the reasoning behind each rule, and the scope boundary. The rules themselves are in `hack/verify-supply-chain/main.go`; the contributor-facing summary is the "Security self-check" bullet in [CONTRIBUTING.md](../../CONTRIBUTING.md), and the reviewer-facing guidance is in `.claude/skills/pr-standards/SKILL.md`.

## What it blocks

| Rule | Matches | Why |
|---|---|---|
| `agent-settings` | `settings.json`, `settings.local.json`, `mcp.json` under any `.claude/` segment; `mcp.json` / `.mcp.json` by basename at any path | These grant an agent standing permission to execute things. The project-scoped `.mcp.json` sits at the repository root with no `.claude` segment to key off, so it is matched by name wherever it appears — which also covers `.cursor/mcp.json` and `.vscode/mcp.json`. |
| `editor-config` | anything under a `.vscode/` segment; any `*.code-workspace` at any path | Several files under `.vscode/` execute on folder open. A `.code-workspace` carries the same payload in one document, under a name of the author's choosing, usually at the repository root. |
| `execution-key` | a `command` or `hooks` key inside one of the agent settings files above | This is the confirmed real-world malware pattern, and is reported as such. Only auto-loaded names are parsed — see [Only auto-discovered config is parsed](#only-auto-discovered-config-is-parsed). |
| `invalid-json` | one of those same files, when it does not parse | See [Malformed JSON is refused](#malformed-json-is-refused-not-scanned-harder). |
| `config-not-a-file` | a `.claude/` or `.vscode/` path whose index mode is not a regular file | See [Path aliases are refused](#path-aliases-are-refused-not-resolved). |
| `unreadable` | agent JSON whose index mode is not a regular file | Same reason, reported against the content rule rather than the path rule. |

The compiled verifier exits 1 when any violation is found and 2 on an internal error. That distinction does not survive `make verify-supply-chain`: the target runs `go run`, which prints `exit status 2` to stderr but itself exits 1, and Make then reports a generic recipe failure. Read the stderr line, not `$?`, or run the binary directly if you need to branch on it.

## Threat model and scope

The check defends against a specific, narrow thing: **a PR that commits configuration which causes an agent or editor to execute something when a reviewer or developer opens the repository.** The reviewer never runs anything deliberately; opening the checkout is enough.

It is deliberately **not** a general-purpose malware scanner, and a green check is not a clean bill of health. The following are explicitly out of scope and remain the reviewer's job:

- **Other agent vendors.** Beyond MCP configuration, which is matched vendor-neutrally by filename, coverage is limited to the agents this repository actually configures. Enumerating vendor directories is a list that is wrong the week a new tool ships while looking complete, so the boundary is stated here rather than implied by a longer list.
- **The contents of `.claude/skills/`,** including its JSON. CONTRIBUTING.md tells contributors to commit shared tooling there. Fixtures, manifests and test data are not parsed for execution keys — see [Only auto-discovered config is parsed](#only-auto-discovered-config-is-parsed) — so a `command` key in a skill asset reaches review unflagged.
- **Agent instruction files.** `AGENTS.md` and `.github/copilot-instructions.md` are legitimately tracked here. They are a prompt-injection surface, not an execution surface, and are not what this check is about.
- **CI/CD configuration.** `.github/workflows/`, `Makefile`, `*pipeline.yaml` and `Dockerfile` changes are not covered. Call them out explicitly in your PR description.

## Design decisions

Each of these is a case where the obvious implementation is the wrong one. They are recorded because each reads like a gap until you know why.

### Only tracked files, read from the index

Paths and modes come from `git ls-files --stage`, never from a filesystem walk. Developers routinely keep a gitignored `.claude/settings.local.json` in their working copy; that is not what this check is about, and walking the tree would fail every local `make verify`.

Being gitignored is not an escape hatch in the other direction: a force-added file is tracked from then on and keeps appearing. The two `SKILL.md` files in this repository are exactly that — committed under a gitignored `/.claude/` directory.

Reading the mode from the index rather than the working tree also keeps the check honest on a fresh clone and on filesystems that do not carry the executable bit.

### Contents come from the object store

File contents are read with `git cat-file blob <oid>`, not `os.ReadFile`.

The two are not the same thing, and only one of them is what a PR proposes. An entry staged as a regular file can be swapped for a symlink afterwards, leaving index mode `100644` while the path on disk points somewhere else entirely — and a benign working-tree copy can just as easily mask a staged `command` key. Reading by object ID closes that gap by construction: the bytes the index names are the bytes scanned.

It also makes the scan unhangable. A blob is inert storage; a working-tree path can be a fifo that blocks forever, which is a real failure mode that was reproduced during review and would have burned a CI job's entire timeout.

### Path aliases are refused, not resolved

A symlink or submodule at a configuration path is reported and never followed.

Git records `frontend/.claude -> config` as a **single** index entry, with no extension and a basename on no denylist, and records nothing at all for the paths underneath. `frontend/.claude/settings.json` is therefore never a tracked path, and no amount of filename matching can ever see it — yet that is precisely the path an agent resolves and reads. A submodule hides it better still: the diff shows a commit ID in a repository the reviewer does not have.

Resolving the alias would mean judging bytes the diff does not contain, which is the opposite of what this check is for. So the entry is refused on the strength of what git records about it, and a human decides. The rule keys on "is not a regular file" rather than on a list of modes, so an entry type nobody anticipated is refused too.

**Reviewers should not resolve these by hand either.** The target may sit outside the diff, or be a device file; a submodule means fetching a repository the author chose. Report the path and its mode and require the alias be removed.

### Only auto-discovered config is parsed

The execution-key rule reads exactly the files the path rules already block by name. It is a **severity escalation**, not a detector: those files are refused either way, and parsing them decides whether the report says "must not be committed" or "matches confirmed malware".

It originally read every `.json` below any `.claude` segment, which swept in skill assets. A manifest documenting example commands, or test data for a skill about Claude Code hooks, was reported as confirmed malware and its author told to contact the security team. A skill that documents hooks *must* contain a `hooks` key; that is not a signal.

The breadth was not buying coverage either. Nothing auto-loads a skill's fixtures, so for one to execute anything a `SKILL.md` has to instruct the agent to read and act on it — and `SKILL.md` is markdown this check does not read. An attacker with that much control writes the command in the markdown. The rule was charging false positives on the team's own tooling for the appearance of covering a route it structurally cannot cover.

The alternative considered was allowlisting `.claude/skills/**`. That was rejected: it exempts a *place*, so a real `settings.json` dropped inside `.claude/skills/x/` would be blocked by path but lose the malware escalation. Keying on the filename instead means an auto-loaded name is escalated wherever it sits, and no directory is a safe harbour.

### Malformed JSON is refused, not scanned harder

The obvious evasion against a check that decodes JSON is to break the syntax — a single trailing comma makes the decoder give up. The tempting fix is to fall back to scanning the raw bytes.

That fallback has to tell a key from a value, and a comment from a string, by hand, guessing at what some other parser would have made of the input. Guessing wrong is expensive here, because a hit tells an author their file matches confirmed malware and sends them to the security team.

So malformed input is refused instead. An attacker is left with valid JSON, where the decoder is authoritative, or invalid JSON, which never reaches the key walk. There is no third case to harden later.

One consequence: strict `encoding/json` means a comment makes a file invalid, which is how `.devcontainer/devcontainer.json` is written. No `.json` is tracked under an agent directory today, and the files most likely to want comments are rejected by path regardless. Should a legitimate agent JSON ever need comments, teach the scanner JSONC deliberately rather than restoring a guess.

### Paths are lowercased before matching

Git's index is case-sensitive, so `.Claude/settings.json` and `.claude/Settings.json` are distinct tracked paths that an agent reads just the same on a case-insensitive filesystem. Every comparison is made against the lowercased path, and every denylist entry is lowercase.

### Paths are quoted in output

Git permits any byte in a path except NUL and the separator — including newlines and ANSI escapes. An unquoted path is therefore a way for the thing being reported on to write the report: a directory named with a newline splits one finding into what reads as several, and one named with an escape sequence can move the cursor up and erase the genuine findings above it.

Neither changes the exit code — the check still fails, and nothing in a path can make it pass — but a security tool whose output can be forged by its subject is not worth reading.

### The editor rule is a directory, not a filename list

`.vscode/` was originally matched by four filenames. Enumerating has only to miss one to be bypassed, and the set of files VS Code executes on folder open belongs to Microsoft. Nothing under `.vscode/` is legitimately tracked here, so the directory itself is the rule.

### No inline override

There is deliberately no comment or annotation that suppresses a finding. Allowing a file this check rejects means editing the check — a visible, reviewable change — rather than a line in the file being allowed.

## Interpreting failures

The report names the path, the rule, and what to do. Two cases need different handling:

- **`execution-key`** is reported as a known attack pattern. Do not open or run the file. Report it to the security team before taking any other action. If it is your own file and the key is innocent, say so in the PR rather than removing the evidence.
- **Everything else** means the file does not belong in the commit. It almost always belongs in your local working copy only — check that it is gitignored, and remember that `git add -f` defeats that permanently.

Run `make verify-supply-chain` locally before pushing; it takes well under a second.

## Extending the check

Add the name to the existing list rather than adding a rule:

- a new auto-loaded agent config filename → `agentSettingsFiles`
- a new MCP config spelling → `mcpConfigFiles`
- a new JSON key that makes config self-executing → `executionKeys`
- a new agent or editor configuration directory → `agentConfigDir` / `editorConfigDir`
- a new editor workspace file extension → `workspaceConfigExt`

`agentSettingsFiles` and `mcpConfigFiles` now drive two rules each: a name on either list is blocked by path *and* parsed for execution keys. That is deliberate — one list, so a newly auto-loaded filename cannot be blocked without also being escalated, or escalated without being blocked. Adding a rule that reads content without a corresponding path rule would reintroduce the split that made skill assets look like malware.

Whichever you add, add a case to `TestCheckPaths` or `TestAgentJSONFiles` with it. `TestRepositoryIsClean` runs the rules against every tracked file, so a change that would flag existing content fails immediately rather than on the next contributor's PR.

## Known limitations

- **The gate lives in the repository it guards.** A PR that edits `hack/verify-supply-chain/`, its `Makefile` wiring, or `go.work` is reviewing its own guard. Those changes need the same manual scrutiny the check automates elsewhere — this is called out in both CONTRIBUTING.md and the reviewer skill.
- **Coverage is by name.** An auto-loaded configuration file nobody has listed passes. That exposure sits on the path rules, which are the actual gate.
- **A clean run proves only that these specific indicators are absent.** It is not a substitute for reading the diff.
