# Stage `stage/parallel/all` manual trigger (do not merge)

This branch exists only to exercise the Prow job
`pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel-all` against the Stage
environment.

## Pinned Stage source revision

The branch is based on the ARO-HCP commit currently associated with Stage
regional gating in UK South:

`238c44f22107b8aaf7d14cb7520d3541f05c5731`

It also carries the `stage/parallel/all` suite definition so the job can select
the combined fast and Slow Stage suite.

## How to run

On the pull request, comment:

```text
/test stage-e2e-parallel-all
```

Close the pull request when the run finishes. Do not merge it into `main`.
