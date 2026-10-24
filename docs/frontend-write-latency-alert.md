# Frontend write median latency alert calibration

`FrontendPathWriteMedianLatency` watches PUT, POST, PATCH, and DELETE separately
from the GET/HEAD median alert. Its candidate threshold is **p50 >200 ms with
at least five requests per route, method, and cluster in 30 minutes**.
The rule uses a five-minute ingestion offset and a one-minute `for` interval.
These values come from DEV CI measurements; the generated rule is deployed to
other monitoring workspaces too. They are not a documented service SLO.

## Measurements

Three DEV `e2e-parallel` runs on 2026-09-29 used source based on main commit
`15f521f` (which includes the admission-scan optimization in #7185). DEV E2E
builds and deploys a job-local frontend image from the tested source; see
[CI image lifecycle](ci/image-lifecycle.md). The linked observability reports
show frontend histogram p50 and 30-minute request count by method, route, and
cluster, sampled every minute:

| Run | Cluster-create PUT p50 samples with count >=5 | Highest eligible cluster-create PUT p50 | Peak 30-minute cluster-create PUT count |
| --- | ---: | ---: | ---: |
| [#7220](https://storage.googleapis.com/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7220/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2104916576108023808/artifacts/e2e-parallel/aro-hcp-gather-observability/artifacts/observability-summary.html) | 53/57 | 18.3 ms | 53.3 |
| [#7221](https://storage.googleapis.com/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7221/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2104871248742846464/artifacts/e2e-parallel/aro-hcp-gather-observability/artifacts/observability-summary.html) | 54/58 | 15.9 ms | 52.6 |
| [#7224](https://storage.googleapis.com/test-platform-results-public/pr-logs/pull/Azure_ARO-HCP/7224/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2104871228572438528/artifacts/e2e-parallel/aro-hcp-gather-observability/artifacts/observability-summary.html) | 54/57 | 17.9 ms | 75.5 |

Among samples with at least five requests, the largest p50 across the three
runs was 39.9 ms for PUT, 15.0 ms for POST, 37.5 ms for PATCH, and 43.5 ms for
DELETE. The 200 ms bound is about 4.6 times the highest observed eligible
write p50 and is independent of the existing 250 ms GET/HEAD alert. It catches
a sustained write regression well below the all-method >1-second slow-request
signal. The previous draft's 1-second write bound was too loose for this data.

At a floor of ten requests, only 95 of 172 cluster-create PUT p50 samples
were eligible across these runs; at five, 161 of 172 were eligible. Five
requests also prevent a single slow call from creating a median alert. This
tradeoff has been measured only for DEV CI traffic. Some sparse routes, including
external-auth PUT (peak about 2.1 requests per 30 minutes), remain below
the floor and need separate evaluation if they require median alert coverage.

The reports' p50 chart uses a 30-minute histogram rate without the alert's
five-minute offset, and its count chart uses `frontend_http_requests_total`
rather than the alert's duration histogram `_count`. These overlapping samples
support threshold selection, but they are not an exact replay of alert firings.
Before merging, check write latency and volume in the other target workspaces
and verify that #7185 has rolled out there. Recheck the bound and volume gate
when traffic patterns or frontend performance change (AROSLSRE-2287).
