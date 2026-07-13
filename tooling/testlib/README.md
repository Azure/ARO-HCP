# E2E Test Conventions Library

This is a library which contains functionality related to E2E tests used (as a
protocol) by other tools, such as hcpctl snapshot.

Behaviour of functions in this package should not be changed without proper
coordination, as both E2E tests and `hcpctl snapshot` tool depends on it, and
requires it to be consistent.

This code would be better placed in test/ directory, but we have few tools in
test/ directory importing code from tooling/hcpctl, and that would create
a circular dependency. So this package is a workaround for that.
