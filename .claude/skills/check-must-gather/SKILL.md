---
name: check-must-gather
description: This skill helps debugging the output of a must gather for errors.
allowed-tools: Read, Grep, Glob, "Bash(ls *)", "Bash(wc *)", "Bash(grep *)", "Bash(cat *)", "Bash(head *)", "Bash(tail *)", "Bash(cd *)", "Bash(for *)", "Bash(python3 *)"
model: claude-opus-4-6
---

Read the must-gather output from $ARGUMENTS[0] and analyze it for errors. You can ask the user of the output of the failed test for addtional references.

The must-gather is organized into folders:

  1. `hosted` contains logs related to the hypershift cluster created. It is a hosted kubernetes controlplane with hypershift additions see https://github.com/openshift/hypershift/ for the source or ask the user for the path to the local version of it.
  2. `service` this folder contains service logs related to the management of beforementioned hosted control planes. these service manage the lifetime of a control plan. See this repository for the source code.

Focus on the `service` logs, as they are the most common case for issues.

## Tool usage guidelines

- Prefer `Grep` for searching log files for error patterns (e.g. `error`, `fatal`, `panic`).
- Prefer `Read` for viewing specific log file contents.
- Prefer `Glob` for discovering files in the must-gather directory.
- Use `Bash` only when you need to do things the built-in tools cannot (e.g. counting lines, parsing JSON logs with python3, sorting/aggregating).
- When using Bash, start commands with one of: `ls`, `wc`, `grep`, `cat`, `head`, `tail`, `cd`, `for`, or `python3`. Do NOT use other command prefixes as they are not allowed.

Ask the user if this was the output of an End to end test.
Ask the user if he can narrow down the number of commits.
