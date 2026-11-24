# Shared GitHub library

This Go module gives existing tools one implementation of verified PR publication,
comments and guarded Prow maintenance. It is a library, not another CLI.

The root `github` package handles GitHub identity, complete evidence reads,
publication and comment receipts. The `prow` package handles bot-hold release,
retest commands and observing whether Prow acted on them.

## Using the library

Existing Go commands import `github.com/Azure/ARO-HCP/tooling/github` and
`github.com/Azure/ARO-HCP/tooling/github/prow`. For example, a caller with an
already pushed branch can publish its complete `PublicationRequest`:

```go
import (
    "context"

    automation "github.com/Azure/ARO-HCP/tooling/github"
    gh "github.com/google/go-github/v69/github"
)

func publish(ctx context.Context, repository, bot, token string, request automation.PublicationRequest) (automation.Receipt, error) {
    api := gh.NewClient(nil).WithAuthToken(token)
    client, err := automation.NewClient(repository, api)
    if err != nil {
        return automation.Receipt{}, err
    }
    client, err = client.WithActor(bot)
    if err != nil {
        return automation.Receipt{}, err
    }
    return client.Publish(ctx, request)
}
```

`WithActor` takes a trusted bot login from caller configuration. This supports
installation tokens without asking GitHub's user-token-only `/user` endpoint to
identify an App. Use `WithWriter` before `WithActor` when read and write clients
have separate credentials.

Use `WithCIReader` when checks and commit statuses require a separate read token.
PR metadata and hold history continue to use the ordinary read client, and
`WithWriter` controls only mutation credentials.

`PublicationRequest` contains the source repository and branch, expected source
and base SHAs, target branch, trusted actor, exact ownership marker, title, body
and complete expected changed-file set. The marker must be a standalone line in
the body. A returned receipt identifies the verified PR and observed head.

When a caller has separately verified an existing PR's old ownership metadata,
`PreviousMarker` permits replacing that exact marker. The write and its read-back
must contain the new `Marker`, not the previous one.

## Caller responsibilities

Callers supply an authenticated `go-github/v69` client. The library does not read
tokens from environment variables, mint App tokens or expand permissions.

Callers also own candidate validation, local commits, branch preparation, schedules
and domain-specific eligibility decisions. Publication uses an already prepared
remote branch at an expected commit; it never stages a dirty checkout or force
pushes. New PRs default to drafts. Set `PublicationRequest.ReadyForReview` only
when the caller's publication policy permits a ready PR; ARO-HCP's
`aro-hcp-robot[bot]` policy requires that setting. Other automation needs explicit
human authorization. Updating a PR always preserves its existing readiness.

Prow callers supply trusted actors, required checks and a budget namespace. A new
library must not give dependency maintenance, security remediation or workflow
recovery a shared or larger retry allowance. Artifact diagnosis and the decision
that a failure is safe to retry remain with the calling tool.

## Safety boundaries

- Match PRs by ownership, repository, branches and a stable marker, never title alone.
- Reject ambiguous ownership, incomplete evidence and unexpected heads.
- Verify a write by reading its result and return an explicit receipt.
- Do not blindly repeat a mutation after a timeout or uncertain response.
- Treat a posted Prow comment as a request, not proof that a hold was removed or a
  new build started.
- GitHub comments have no atomic expected-head precondition. Head checks reduce
  races but cannot provide compare-and-swap or exactly-once execution.
- Do not cancel human or unexplained holds or provide approval and merge bypasses.

`depctl dependabot native publish` uses this library for replacement PR
publication. Other consumers can adopt it through their existing Go commands
without introducing a helper executable.

## Prow maintenance

Construct `prow.New(client, config)` with the target repository, trusted writer,
retry bots, hold controllers, required checks and a distinct budget namespace.
`ReleaseHold` accepts only proven bot retry-exhaustion holds whose revision belongs
to the PR's commits and whose current required CI is positive. Optional check
conclusions do not gate maintenance; missing, pending, neutral or skipped required
checks still block. `Retest` requires a current
required-check failure, a distinct same-job comparison URL and a plain-text reason.
The caller verifies comparison artifacts and diagnosis; the library validates URL
syntax and association, not the comparison run's existence or outcome.

Named requests identify the canonical job from Prow evidence. Trusted caller
configuration supplies `Config.JobTriggers`, a canonical-job-to-alias map with
unique aliases, for example `pull-ci-Azure-ARO-HCP-main-api-validation` to
`api-validation`. Named requests without a mapping are rejected. The library
never guesses aliases by stripping prefixes, falls back to canonical names or
accepts arbitrary command text.
Required-set requests use `/retest-required`.

Publication supports forks and defaults to draft creation, but Prow maintenance
deliberately accepts only open, non-draft PRs whose head repository is the target
repository. Publishing a PR does not automatically make it eligible for Prow
maintenance; draft and fork consumers must retain that boundary.

A root `PostComment` receipt with `observed` means only that the comment was read
back successfully. Prow converts that acknowledgement to `requested`. Pass the
intact result to `Observe` with a context deadline to verify label removal or a
new current-head run of a currently eligible job. Historical runs form the
already-seen baseline, not additional observation targets. Observation does not
prove that this comment caused the run or that the run passed.
`indeterminate` requires reconciliation, not blind retries.

Each action collects one complete eligibility snapshot. Observation reads only PR
state for hold release, or head-bound statuses/checks with PR identity bookends
for retests; it does not repeatedly fetch comments, commits and label history.

Retest comments retain a bounded, versioned record of their original targets and
already-seen runs. Calling `Retest` again reconciles the existing request without
posting another comment and restores that observation baseline, so a targeted run
that started before a process restart can still be observed. Edited or malformed
records fail closed. Older comments without baseline metadata retain their
consumed budget but cannot prove that a run already started before resumption.

When adopting this API, a consumer must reconcile its existing recovery markers
and cooldown rules before enabling writes. A new namespace is not permission to
reset an existing retry budget.

## Tests

```sh
GOWORK=off go test -race ./...
```

Tests use local HTTP fixtures and injected evidence. They do not need GitHub
credentials or send mutations to a real repository.
