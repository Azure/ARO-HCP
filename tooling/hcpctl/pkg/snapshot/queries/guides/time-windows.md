# Time windows

Every query filters on a time window, both for correctness and because Kusto prunes data
by time.

- **FullStartTime / FullEndTime** is the investigation window. Discovery queries (the ones
  that provide identifiers) search all of it. Choose it from the objective, with margin:
  a cluster's history usually starts at its creation, and problems are often visible
  before they are reported.
- **PhaseStartTime / PhaseEndTime** is a narrower window used by state, condition, log and
  event queries. It defaults to the investigation window. Narrow it to focus on one
  episode, for example the minutes around a failure, without losing the discovery
  queries' wider view.

Values are datetimes, preferably in RFC3339 UTC (`2025-01-02T15:04:05Z`).

Narrow windows reduce noise but can hide causes. If a query returns nothing, widen the
window before concluding that something did not happen, and use
`| summarize count()` to prove absence.
