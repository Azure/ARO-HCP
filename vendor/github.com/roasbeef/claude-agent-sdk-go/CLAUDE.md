# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test Commands

```bash
go build ./...                          # Build
go test ./...                           # Unit tests
go test -v -run TestName ./...          # Single test
make lint                               # Run linter (must pass)
make test-integration                   # Integration tests (needs API token)
make test-race                          # Unit tests with race detector
```

Integration tests require `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`. Without a token, they skip (not fail). Always run `make test-integration` after changes that affect CLI communication.

The SDK has been brought forward through the v0.2.119 TypeScript Agent SDK catchup. When adding new Claude Code CLI surfaces, check the installed CLI behavior and the TypeScript SDK reference before assuming an older local pattern is complete.

## Verifying Changes

**All changes must pass tests and lint before committing.**

After modifying code:
1. `go build ./...` - must pass
2. `go test ./...` - unit tests must pass
3. `make lint` - linter must pass (no warnings)
4. `make test-integration` - run if touching client.go, protocol.go, transport.go, settings/options, or message handling

For new features, check coverage: `make coverage`

## Architecture

This SDK wraps the Claude Code CLI as a subprocess, communicating via JSON over stdin/stdout. See [docs/DESIGN.md](docs/DESIGN.md) for the full architecture and [docs/cli-protocol.md](docs/cli-protocol.md) for the wire protocol.

**Key files**:
- `client.go` - `Client`, `Stream`, `Query()` using `iter.Seq[Message]`
- `protocol.go` - Control messages, hooks, MCP routing
- `transport.go` - Subprocess lifecycle, stdin/stdout pipes
- `mcp.go` - In-process MCP server with generics-based tools
- `ask_user.go` - `QuestionMessage` for interactive Q&A
- `sessions.go` - Local JSONL session listing, loading, and deletion helpers
- `tasks.go` - Persistent task-list helpers shared with Claude Code task tools

## Code Style

- Use `go doc` to look up APIs when unsure
- Comments are complete sentences ending with periods
- Comments focus on the why/how instead of the plain what

## Integration Test Patterns

```go
opts := append(isolatedClientOptions(t),
    WithSystemPrompt("..."),
    WithPermissionMode(PermissionModeBypassAll),
)
```

Use `skipIfNoToken(t)` and `skipIfNoCLI(t)` at the start.
