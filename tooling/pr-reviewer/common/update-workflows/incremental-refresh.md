# Incremental Refresh Workflow

The reviewer should stay current via a loop, not a one-time bootstrap.

## Bootstrap

- Run `common/tools/bootstrap_history.py --since <date> --output <dir>`.
- For the canonical local source tree, point the collector at `~/ARO-HCP` and the mainline ref explicitly, for example:

  ```bash
  python3 tooling/pr-reviewer/common/tools/bootstrap_history.py \
    --repo-root ~/ARO-HCP \
    --git-ref origin/main \
    --since 2025-09-25 \
    --output tooling/pr-reviewer/fixtures/history/bootstrap.json
  ```

- Review the produced corpus against `common/schema/history-corpus.schema.json`.
- Convert high-value lessons into authoritative assets or fixtures.

## Ongoing update loop

1. ingest newly merged PRs, comments, and reviews
2. classify changed files using `common/domain-routing/path-routing.json`
3. update coverage/staleness metadata
4. extract any durable new lesson into `common/` or `sub-reviewers/`
5. add or update a fixture and eval when the lesson matters enough to preserve

## Completion rule

A new lesson is not complete until the authoritative asset changed and a fixture/eval reflects it.
