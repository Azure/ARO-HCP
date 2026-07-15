---
applyTo: "test/**"
---

# E2E Test Code Review Instructions

When reviewing E2E test code changes, enforce ALL rules from these authoritative sources:

## Required Standards (in order)

1. **[`../../CONTRIBUTING.md#pull-request-standards`](../../CONTRIBUTING.md#pull-request-standards)** — General PR standards for all code
2. **[`../../.github/copilot-instructions.md`](../copilot-instructions.md)** — Base Copilot PR review rules
3. **[`../../test/AGENTS.md`](../../test/AGENTS.md)** — E2E test design principles, best practices, and code review standards

## Quick Reference

The [`test/AGENTS.md`](../../test/AGENTS.md) file is the **single source of truth** for E2E testing standards. It contains:

- **Principles of Good E2E Test Case Design**: Cluster provisioning, resource naming, SDK client usage, verifiers, cleanup
- **Best Practices**: Assertion messages, logging (including delta-only logging for polling), labels, file structure
- **Tips and Tricks**: Running/filtering tests, randomization, development environment testing
- **E2E Test Code Review Standards**: Build tags, test structure, required labels, client patterns, anti-patterns, review checklist

Read and apply all sections when reviewing E2E test PRs.
