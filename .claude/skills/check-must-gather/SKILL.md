---
name: check-must-gather
description: This skill helps debugging the output of a must gather for errors.
allowed-tools: Read, Grep, Glob, Agent, "Bash(ls *)", "Bash(wc *)", "Bash(grep *)", "Bash(cat *)", "Bash(head *)", "Bash(tail *)", "Bash(cd *)", "Bash(for *)", "Bash(python3 *)"
model: claude-opus-4-6[1m]
---

Read the must-gather output from $ARGUMENTS[0] and analyze it for errors. You can ask the user of the output of the failed test for addtional references.

The must-gather is organized into folders:

  1. `hosted-control-plane` contains logs related to the hypershift cluster created. It is a hosted kubernetes controlplane with hypershift additions see https://github.com/openshift/hypershift/ for the source or ask the user for the path to the local version of it.
  2. `service` this folder contains service logs related to the management of beforementioned hosted control planes. these service manage the lifetime of a control plan. See this repository for the source code.

Focus on the `service` logs, as they are the most common case for issues.

## Context

There is additional documentation, that helps understanding the system read:
- docs/terminology.md
- docs/high-level-architecture.md

## Tool usage guidelines

- Use `Bash` only when you need to do things the built-in tools cannot (e.g. counting lines, parsing JSON logs with python3, sorting/aggregating).
- When using Bash, start commands with one of: `ls`, `wc`, `grep`, `cat`, `head`, `tail`, `cd`, `for`, or `python3`. Do NOT use other command prefixes as they are not allowed.

## Logs consumption

Use `eb` cli consume logs:
```bash
error-buddies reads log lines from stdin or searches files for patterns, normalizes them, and groups similar lines using the Drain algorithm.

Usage:
  error-buddies [flags]

Flags:
      --custom-replacement stringArray   add custom replacement in format 'pattern=replacement' (can be used multiple times)
  -d, --depth int                        drain tree depth (minimum 3) (default 4)
      --disable-normalization            disable all log normalization
  -h, --help                             help for error-buddies
  -m, --max-clusters int                 maximum number of clusters to track, if the number of clusters exceeds this value, the oldest cluster will be removed (LRU cache) (default 10000)
  -o, --outdir string                    output directory for cluster files (used with --output=dir) (default "clustered_errors")
  -f, --output string                    output format: dir, json, or html (default "dir")
  -s, --search-dir string                directory to search for files (mutually exclusive with stdin)
  -p, --search-pattern string            pattern to search for in files (required with --search-dir)
  -t, --sim-threshold float              similarity threshold for clustering (0.0-1.0) (default 0.5)
      --use-replacements string          comma-separated list of replacement sets (default,timestamp,uuid,hex,number,klog,network,all) (default "default")
```

Example, get all errors in `.` directory as json:
```
eb -s . -p '.*[eErRoO]{5}.*' -f json
```