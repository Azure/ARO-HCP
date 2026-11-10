# Supply-Chain Presubmit (`make verify-supply-chain`)

`hack/verify-supply-chain/` is a Go tool that inspects every file tracked by git and fails if any of them matches a high-confidence supply-chain attack indicator — a committed AI-agent settings file, MCP server configuration, or editor configuration. It is a dependency of `make verify`, so it runs on every PR through the always-on `ci/prow/verify` presubmit, and it is the first dependency in that list so it fails in under a second rather than after the multi-minute codegen checks.

This page documents the threat model, the reasoning behind each rule, and the scope boundary. The rules themselves are in `hack/verify-supply-chain/main.go`; the contributor-facing summary is the "Security self-check" bullet in [CONTRIBUTING.md](../../CONTRIBUTING.md), and the reviewer-facing guidance is in `.claude/skills/pr-standards/SKILL.md`.

## An interim measure

This check is a stopgap, and is documented as one. It automates the highest-confidence slice of review guidance that was previously advisory only ([#5563](https://github.com/Azure/ARO-HCP/pull/5563)), and it lives in this repository because that was the only place to automate anything quickly — which is also its central weakness, since a PR can weaken its own gate. It is not intended as a long-term fixture, and it must not grow into a general-purpose security scanner.

The intended replacement is off-the-shelf or platform-provided enforcement that lives outside this repository and does not ask this team to track agent-configuration attack vectors itself. When that exists, this tool should be deleted rather than brought up to meet it. Until then its maintenance cost stays deliberately bounded: a handful of filename lists, a sub-second runtime, and a test suite that fails CI the moment a rule grows too broad for this repository's own contents.

## What it blocks

| Rule | Matches | Why |
|---|---|---|
| `agent-settings` | `settings.json`, `settings.local.json`, `mcp.json`, `hooks.json` under any `.claude/`, `.copilot/` or `.cursor/` segment; `mcp.json` / `.mcp.json` by basename at any path | These grant an agent standing permission to execute things. The project-scoped `.mcp.json` sits at the repository root with no agent segment to key off, so it is matched by name wherever it appears — which also covers `.vscode/mcp.json`. The filenames are matched under every agent directory rather than per vendor: `hooks.json` is Cursor's spelling today, and a name that means "run this" under one agent is not worth waiting for a second to adopt. |
| `editor-config` | anything under a `.vscode/` segment; any `*.code-workspace` at any path | Several files under `.vscode/` execute on folder open. A `.code-workspace` carries the same payload in one document, under a name of the author's choosing, usually at the repository root. |
| `config-not-a-file` | any agent or editor configuration path whose index mode is not a regular file | See [Path aliases are refused](#path-aliases-are-refused-not-resolved). |

Every rule is decided from the git index — the path and the mode. **Nothing reads file contents.** A blocked filename is blocked whatever is inside it, so the only thing reading one could change is the wording of a failure that has already happened. See [The check never reads file contents](#the-check-never-reads-file-contents).

The compiled verifier exits 1 when any violation is found and 2 on an internal error. That distinction does not survive `make verify-supply-chain`: the target runs `go run`, which prints `exit status 2` to stderr but itself exits 1, and Make then reports a generic recipe failure. Read the stderr line, not `$?`, or run the binary directly if you need to branch on it.

## Threat model and scope

The check defends against a specific, narrow thing: **a PR that commits configuration which causes an agent or editor to execute something when a reviewer or developer opens the repository.** The reviewer never runs anything deliberately; opening the checkout is enough.

It is deliberately **not** a general-purpose malware scanner, and a green check is not a clean bill of health. The following are explicitly out of scope and remain the reviewer's job:

- **Agent vendors beyond `.claude/`, `.copilot/` and `.cursor/`.** Those three are the directories a contributor here plausibly has configured, which is where an accidental commit comes from; within them the settings filenames are matched vendor-neutrally, so a name one agent adds is covered for all three at once. The list stops there on purpose. Enumerating vendors is a list that goes stale the week a new tool ships while still looking complete, and a longer one would imply a completeness this cannot have — so the boundary is stated here instead. A fourth vendor's directory passes until someone adds it; MCP configuration is the exception, matched by filename at any path and so already covered wherever it sits.
- **The contents of `.claude/skills/`,** and of every other file. CONTRIBUTING.md tells contributors to commit shared tooling there, and nothing is read to second-guess it, so a `command` key in a skill asset reaches review unflagged. The one thing that still applies inside `skills/` is the filename: `.claude/skills/x/settings.json` is blocked like any other, because the rules key on the name and no directory is a safe harbour.
- **Agent instruction files.** `AGENTS.md`, `.github/copilot-instructions.md` and `.cursor/rules/` are legitimately tracked here. They are a prompt-injection surface, not an execution surface, and are not what this check is about. Note the asymmetry with `.vscode/`, which is blocked wholesale: an agent directory is a place this repository expects content, so only the named files in it are rejected.
- **CI/CD configuration.** `.github/workflows/`, `Makefile`, `*pipeline.yaml` and `Dockerfile` changes are not covered. Call them out explicitly in your PR description.

## Design decisions

Each of these is a case where the obvious implementation is the wrong one. They are recorded because each reads like a gap until you know why.

### Only tracked files, read from the index

Paths and modes come from `git ls-files --stage`, never from a filesystem walk. Developers routinely keep a gitignored `.claude/settings.local.json` in their working copy; that is not what this check is about, and walking the tree would fail every local `make verify`.

Being gitignored is not an escape hatch in the other direction: a force-added file is tracked from then on and keeps appearing. The two `SKILL.md` files in this repository are exactly that — committed under a gitignored `/.claude/` directory.

Reading the mode from the index rather than the working tree also keeps the check honest on a fresh clone and on filesystems that do not carry the executable bit. The index and the working tree can disagree — an entry staged as a regular file can be swapped for a symlink afterwards, leaving mode `100644` while the path on disk points elsewhere — and only the index is what the PR actually proposes.

A side effect worth keeping: the check never opens a path on disk, so it cannot be made to hang. A working-tree path can be a fifo that blocks forever, a failure mode reproduced during review that would have burned a CI job's entire timeout.

### Path aliases are refused, not resolved

A symlink or submodule at a configuration path is reported and never followed.

Git records `frontend/.claude -> config` as a **single** index entry, with no extension and a basename on no denylist, and records nothing at all for the paths underneath. `frontend/.claude/settings.json` is therefore never a tracked path, and no amount of filename matching can ever see it — yet that is precisely the path an agent resolves and reads. A submodule hides it better still: the diff shows a commit ID in a repository the reviewer does not have.

Resolving the alias would mean judging bytes the diff does not contain, which is the opposite of what this check is for. So the entry is refused on the strength of what git records about it, and a human decides. The rule keys on "is not a regular file" rather than on a list of modes, so an entry type nobody anticipated is refused too.

**Reviewers should not resolve these by hand either.** The target may sit outside the diff, or be a device file; a submodule means fetching a repository the author chose. Report the path and its mode and require the alias be removed.

### The check never reads file contents

Every rule is decided from `git ls-files --stage`: the path and the index mode. No blob is ever resolved, no JSON is ever decoded.

An earlier version did parse the agent settings files, to decide whether the report said "must not be committed" or "matches confirmed malware". That verdict was never load-bearing. The parser read *exactly* the filenames the path rules already block — the two selectors were the same two conditions — so it could not change whether a PR passed, only the words used to fail it.

And it was a verdict the check cannot support. A committed `.claude/settings.json` containing `{"hooks":[{"command":"..."}]}` is byte-identical whether an attacker planted it or a colleague forgot that `.gitignore` only anchors `/.claude/` at the root. What separates malware from a mistake is provenance, which is not in the file. On a repository of trusted contributors the second is far likelier, so the check was asserting the wrong one by default.

Reading contents also produced every false positive this check has had: skill fixtures reported as confirmed malware, a blanket remediation telling authors to delete legitimate assets, and an empty `{"hooks":{}}` sending someone to the security team. The path rules have produced none.

What is lost is the ability to say *which* blocked file holds something that would actually run. The report compensates with a line printed for every finding — do not open or run one of these that is not yours — which is true of all of them and claims nothing about intent.

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

- **If the file is yours**, remove it from the commit — it belongs in your local working copy. Check that it is gitignored, and remember that `git add -f` defeats that permanently.
- **If it is not yours**, do not open or run it. This configuration executes on clone, which is how a known supply-chain attack worked. Report it to the security team rather than investigating it yourself. The check does not read these files, so it cannot tell you which one is dangerous — that is the reason for the caution, not an argument against it.
- **Everything else** means the file does not go in the commit as it stands, but the fix differs by rule and the report prints the right one under "What to do". A settings or editor file belongs in your local working copy — check it is gitignored, and remember that `git add -f` defeats that permanently. An alias is different: a symlinked asset under `.claude/skills/` is usually legitimate content in the wrong shape, and materialising it as a real file, or moving it off the configuration path, is the fix rather than deleting it.

Run `make verify-supply-chain` locally before pushing; it takes well under a second.

## Extending the check

First, remember what this is: an interim measure, not a product. Every addition is maintenance carried forward until the replacement lands, so add a name only when it closes a real gap in the lists below — not because a vector can be named. The scope here crept once already, during review; it should not creep again.

Add the name to the existing list rather than adding a rule:

- a new auto-loaded agent config filename → `agentSettingsFiles`
- a new MCP config spelling → `mcpConfigFiles`
- a new agent or editor configuration directory → `agentConfigDirs` / `editorConfigDirs`
- a new editor workspace file extension → `workspaceConfigExt`

Resist adding a rule that reads file contents. That is where this check's maintenance cost and every one of its false positives came from, and the outcome is already decided by the name.

Whichever you add, add a case to `TestCheckPaths` with it. `TestRepositoryIsClean` runs the rules against every tracked file, so a change that would flag existing content fails immediately rather than on the next contributor's PR.

## Known limitations

- **The gate lives in the repository it guards.** A PR that edits `hack/verify-supply-chain/`, its `Makefile` wiring, or `go.work` is reviewing its own guard. Those changes need the same manual scrutiny the check automates elsewhere — this is called out in both CONTRIBUTING.md and the reviewer skill.
- **Coverage is by name.** An auto-loaded configuration file nobody has listed passes. That exposure sits on the path rules, which are the actual gate.
- **A clean run proves only that these specific indicators are absent.** It is not a substitute for reading the diff.
