# Supply-Chain Presubmit (`make verify-supply-chain`)

`hack/verify-supply-chain/` is a Go tool that inspects every file tracked by git and fails if any of them is unreviewed agent or editor configuration. The rule is **default-deny**: nothing in an agent or editor configuration location may be committed unless the allowlist in the tool's own source approves that exact path at that exact content. It is a dependency of `make verify`, so it runs on every PR through the always-on `ci/prow/verify` presubmit, and it is the first dependency in that list so it fails in under a second rather than after the multi-minute codegen checks.

This page documents the threat model, the reasoning behind each rule, and the scope boundary. The rules themselves are in `hack/verify-supply-chain/main.go`; the contributor-facing summary is the "Security self-check" bullet in [CONTRIBUTING.md](../../CONTRIBUTING.md), and the reviewer-facing guidance is in `.claude/skills/pr-standards/SKILL.md`.

## An interim measure

This check is a stopgap, and is documented as one. It automates the highest-confidence slice of review guidance that was previously advisory only ([#5563](https://github.com/Azure/ARO-HCP/pull/5563)), and it lives in this repository because that was the only place to automate anything quickly — which is also its central weakness, since a PR can weaken its own gate. It is not intended as a long-term fixture, and it must not grow into a general-purpose security scanner.

The intended replacement is off-the-shelf or platform-provided enforcement that lives outside this repository and does not ask this team to track agent-configuration attack vectors itself. When that exists, this tool should be deleted rather than brought up to meet it. Until then its maintenance cost stays deliberately bounded: a short list of locations, a six-entry allowlist, a sub-second runtime, and a test suite that fails CI the moment a rule stops matching this repository's own contents.

## What it blocks

| Rule | Matches | Why |
|---|---|---|
| `unlisted-config` | anything in a configuration location whose exact path is not in `allowedConfigFiles` | The default. See [Default-deny and content-pinned](#default-deny-and-content-pinned). |
| `modified-config` | an approved path whose blob no longer matches the approved object ID | Approval covers content, not just the name. |
| `config-not-a-file` | any path in a configuration location whose index mode is not a regular file | Checked *before* the allowlist, so an approved path cannot launder a symlink. See [Path aliases are refused](#path-aliases-are-refused-not-resolved). |
| `agent-settings` | `mcp.json` / `.mcp.json` by basename at any path | The project-scoped form sits at the repository root, outside every configuration location. |
| `editor-config` | any `*.code-workspace` at any path | Carries the whole of `.vscode/` in one document, under a name of the author's choosing, and also usually at the root. |

Configuration locations are the `.claude/`, `.copilot/`, `.cursor/` and `.vscode/` path segments at any depth, plus the `.github/agents/`, `.github/skills/` and `.github/instructions/` prefixes. The rest of `.github/` is out of scope — see [Known limitations](#known-limitations).

Every rule is decided from the git index: paths, modes, and the object IDs git has already computed. **No blob is read and nothing is parsed.** See [The check never reads file contents](#the-check-never-reads-file-contents).

The compiled verifier exits 1 when any violation is found and 2 on an internal error. That distinction does not survive `make verify-supply-chain`: the target runs `go run`, which prints `exit status 2` to stderr but itself exits 1, and Make then reports a generic recipe failure. Read the stderr line, not `$?`, or run the binary directly if you need to branch on it.

## Threat model and scope

The check defends against a specific, narrow thing: **a PR that commits configuration which causes an agent or editor to execute something when a reviewer or developer opens the repository.** The reviewer never runs anything deliberately; opening the checkout is enough.

It is deliberately **not** a general-purpose malware scanner, and a green check is not a clean bill of health. The following are explicitly out of scope and remain the reviewer's job:

- **Agent vendors beyond the listed locations.** `.claude/`, `.copilot/` and `.cursor/` are the directories a contributor here plausibly has configured, which is where an accidental commit comes from; the `.github/` subtrees cover what this repository already ships. The list stops there on purpose: enumerating vendors goes stale the week a new tool ships while still looking complete, and a longer list would imply a completeness this cannot have. Another vendor's directory passes until someone adds it. MCP configuration is the exception, matched by filename at any path and so already covered wherever it sits.
- **The contents of every file, allowlisted ones included.** Nothing is parsed. An allowlisted `SKILL.md` is prose an agent reads and follows: it needs no executable bit and no script, and no rule here inspects it. Approving an allowlist entry means someone read the file; the check only records that they said they did.
- **Agent instruction files outside the configuration directories.** `AGENTS.md` and `.github/copilot-instructions.md` are legitimately tracked here and are not covered. They are a prompt-injection surface rather than an execution one. Anything inside a configuration location is covered, instruction file or not — `.cursor/rules/` and `.github/instructions/` included.
- **CI/CD configuration.** `.github/workflows/`, `Makefile`, `*pipeline.yaml` and `Dockerfile` changes are not covered. Call them out explicitly in your PR description.

## Design decisions

Each of these is a case where the obvious implementation is the wrong one. They are recorded because each reads like a gap until you know why.

### Only tracked files, read from the index

Paths, modes and object IDs come from `git ls-files --stage`, never from a filesystem walk. Developers routinely keep a gitignored `.claude/settings.local.json` in their working copy; that is not what this check is about, and walking the tree would fail every local `make verify`.

Being gitignored is not an escape hatch in the other direction: a force-added file is tracked from then on and keeps appearing. Every file tracked under `/.claude/` in this repository is exactly that — force-added past a gitignore.

Reading the mode from the index rather than the working tree also keeps the check honest on a fresh clone and on filesystems that do not carry the executable bit. The index and the working tree can disagree — an entry staged as a regular file can be swapped for a symlink afterwards, leaving mode `100644` while the path on disk points elsewhere — and only the index is what the PR actually proposes.

A side effect worth keeping: the check never opens a path on disk, so it cannot be made to hang. A working-tree path can be a fifo that blocks forever, a failure mode reproduced during review that would have burned a CI job's entire timeout.

### Default-deny and content-pinned

A denylist of dangerous filenames cannot hold: an agent loads what its own configuration points it at, so a payload moves to a name the list never heard of. `.claude/skills/x/my-hooks.json` carried the identical payload past a list that blocked `.claude/settings.json`.

So nothing in a configuration location is committed unless someone put its exact path on the allowlist, and approval is pinned to the object ID git recorded for the blob. Editing an approved file changes that ID and revokes the approval until it is renewed. Without the pin, the allowlist would publish the set of files an attacker may edit freely — appending an instruction to a `SKILL.md` an agent already trusts needs no new file, no executable bit and no script.

Checking the object ID reads nothing. It is the identifier git already computed, and it answers only "are these the approved bytes?".

The allowlist lives in `main.go` rather than a data file. Read from a data file it would be configuration the gate obeys, and adding a wildcard there is a quieter diff than changing the verifier — which is the review the list exists to force.

**What this does not do** is prevent a malicious addition or edit. Either one can ship with its own allowlist entry in the same PR and CI goes green. It makes the change declare itself in a security-flagged place; it is a visibility mechanism, not a barrier.

### Path aliases are refused, not resolved

A symlink or submodule at a configuration path is reported and never followed.

Git records `frontend/.claude -> config` as a **single** index entry, with no extension and a basename on no denylist, and records nothing at all for the paths underneath. `frontend/.claude/settings.json` is therefore never a tracked path, and no amount of filename matching can ever see it — yet that is precisely the path an agent resolves and reads. A submodule hides it better still: the diff shows a commit ID in a repository the reviewer does not have.

Resolving the alias would mean judging bytes the diff does not contain, which is the opposite of what this check is for. So the entry is refused on the strength of what git records about it, and a human decides. The rule keys on "is not a regular file" rather than on a list of modes, so an entry type nobody anticipated is refused too.

**Reviewers should not resolve these by hand either.** The target may sit outside the diff, or be a device file; a submodule means fetching a repository the author chose. Report the path and its mode and require the alias be removed.

### The check never reads file contents

No blob is resolved and no JSON is decoded. An earlier version parsed the agent settings files to decide whether to call a finding malware, which could not change any outcome — the files were already refused — and could not be supported: a committed `settings.json` is identical whether a colleague forgot to gitignore it or an attacker planted it. Provenance is not in the file.

Reading contents also produced every false positive this check has had. The path rules have produced none.

What is lost is knowing which blocked file holds something that would run. The report compensates with a line printed for every finding — do not open or run one that is not yours — which is true of all of them and claims nothing about intent.

### Case is handled in opposite directions on purpose

Git's index is case-sensitive, so `.Claude/skills/…` and `.claude/skills/…` are distinct tracked paths that an agent may read identically on a case-insensitive filesystem. The two lookups therefore disagree deliberately:

- The **directory match is case-insensitive**, so `.Claude/` cannot evade the rule.
- The **allowlist lookup is case-sensitive**, so a near-miss spelling cannot inherit an approved path's approval.

Lowercasing both would quietly admit every case variant of every allowlisted path. Lowercasing neither would let `.Claude/anything` through. `TestAllowlistIsCaseSensitive` pins both halves.

### Paths are quoted in output

Git permits any byte in a path except NUL and the separator — including newlines and ANSI escapes. An unquoted path is therefore a way for the thing being reported on to write the report: a directory named with a newline splits one finding into what reads as several, and one named with an escape sequence can move the cursor up and erase the genuine findings above it.

Neither changes the exit code — the check still fails, and nothing in a path can make it pass — but a security tool whose output can be forged by its subject is not worth reading.

### No inline override

There is deliberately no comment or annotation that suppresses a finding from inside the file being checked. The only way to allow a path is the allowlist in `main.go` — a visible, reviewable change to a security control, in the same PR as the file it approves.

## Interpreting failures

The report names the path, the rule, and what to do.

- **If it is not yours**, do not open or run it. This configuration executes on clone, which is how a known supply-chain attack worked. Report it to the security team rather than investigating it yourself. The check does not read these files, so it cannot tell you which one is dangerous — that is the reason for the caution, not an argument against it.
- **If the file does not belong in the repository**, remove it; agent and editor configuration belongs in your local working copy. Check it is gitignored, and remember that `git add -f` defeats that permanently.
- **If it genuinely does belong** — a new shared skill, say — it needs an allowlist entry. Confirm the file is needed, confirm it cannot execute anything by itself, have a second person read it in full, and add its exact path to `allowedConfigFiles` in the same PR. Say in the PR description that you are doing so and why.
- **If an approved file has changed**, its approval no longer applies. Read the diff in full before anything else, then update the pinned hash with `git hash-object <path>` in the same PR if the change is legitimate.
- **An alias** is different again: a symlinked asset under a configuration directory is usually legitimate content in the wrong shape. Materialise it as a real file, or move it off the configuration path. Do not resolve it to see where it pointed.

Run `make verify-supply-chain` locally before pushing; it takes well under a second.

## Extending the check

First, remember what this is: an interim measure, not a product. Every addition is maintenance carried forward until the replacement lands, so add a name only when it closes a real gap in the lists below — not because a vector can be named. The scope here crept once already, during review; it should not creep again.

Add to the existing list rather than adding a rule:

- a reviewed file that must be committed, or a new hash for one → `allowedConfigFiles`
- a new agent or editor configuration directory → `configDirs`
- a configuration location whose directory name is too generic to match as a segment → `configPrefixes`
- a new MCP config spelling → `mcpConfigFiles`
- a new editor workspace file extension → `workspaceConfigExt`

Resist adding a rule that reads file contents. That is where this check's maintenance cost and every one of its false positives came from, and the outcome is already decided by the path.

Whichever you add, add a case to `TestCheckPaths`. `TestAllowlistMatchesTheRepository` fails on an allowlist entry whose file no longer exists *and* on a pin that no longer matches the index, so approvals cannot drift from reality; `TestRepositoryIsClean` runs the rules against every tracked file so an over-broad change fails here rather than on the next contributor's PR.

## Known limitations

- **The gate lives in the repository it guards.** A PR that edits `hack/verify-supply-chain/`, its `Makefile` wiring, or `go.work` is reviewing its own guard. Those changes need the same manual scrutiny the check automates elsewhere — this is called out in both CONTRIBUTING.md and the reviewer skill.
- **An allowlist entry is an assertion, not a proof.** It records that someone said they read that content. Nothing verifies that; the pin only guarantees the bytes have not changed since.
- **Coverage stops at the listed locations.** A new vendor's configuration directory passes until someone adds it. MCP configuration is the exception, matched by filename wherever it sits.
- **Most of `.github/` is out of scope.** Only `agents/`, `skills/` and `instructions/` are covered. `workflows/` and `scripts/` are high-churn CI configuration — an allowlist turning over daily would be one nobody reads — and are reviewer territory, as `CONTRIBUTING.md` says.
- **`.vscode/` applies to every contributor,** where [ARO-29001](https://issues.redhat.com/browse/ARO-29001) asked only for external ones. Author identity is not available to a check that runs from the git index in `make verify`, and relaxing the rule for members would weaken it against a compromised account while disabling it for the people most likely to commit editor config by accident. Nothing under `.vscode/` is legitimately tracked, so the stricter rule costs nobody anything.
- **A clean run proves only that these specific indicators are absent.** It is not a substitute for reading the diff.
