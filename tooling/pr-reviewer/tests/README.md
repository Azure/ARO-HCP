# Reviewer Tests

The first test layer for this reviewer is the eval set in `../evals/evals.json` plus the seed fixtures in `../fixtures/`.

For automated behavioral scoring, run `make -C tooling/pr-reviewer evalcheck SELECTION=mixed` or target specific eval ids such as `SELECTION=13`.

That runner executes the reviewer headlessly with the local `claude` CLI and grades the review output against the eval expectations, so it complements `make -C tooling/pr-reviewer validate` rather than replacing it.

`make -C tooling/pr-reviewer validate` uses `tests/history-corpus-smoke.json` for history-corpus schema coverage. Large generated bootstrap or backfill corpora should stay local and should not be committed to the reviewer package.

Every durable rule should eventually have:

- a fixture showing the historical rationale
- an eval prompt that exercises the behavior
- calibration notes if reviewer quality or style is part of the lesson
