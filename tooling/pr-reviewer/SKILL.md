---
name: aro-hcp-pr-reviewer
description: Perform deep, repo-specific review for ARO-HCP pull requests, commit ranges, or risky file sets. Use this whenever the user asks to review an ARO-HCP PR, inspect a change in Azure/ARO-HCP, understand why reviewers would care about a change, or wants rollout-safety, correctness, config, Bicep, pipeline, testing, or observability analysis for ARO-HCP.
compatibility:
  tools:
    - view
    - rg
    - glob
    - bash
    - github-mcp-server-pull_request_read
    - github-mcp-server-search_pull_requests
disable-model-invocation: true
context: fork
agent: aro-hcp-pr-reviewer-main
---

# ARO-HCP PR Reviewer

Launch the in-repo ARO-HCP reviewer in a fresh context so it can inspect a pull request, commit range, or risky path set without reusing the caller's working context.

Use the current user request as the review target.

Review target: `$ARGUMENTS`
