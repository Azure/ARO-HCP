# Cosmos Query Telemetry

Cosmos request metrics attribute reported RU and HTTP responses to the calling
controller or informer, container, operation, status, call site, query shape, and
partition scope. See [RU attribution](cosmos-data-flow.md#request-unit-ru-attribution)
for the existing retry-inclusive counters and source precedence.

This telemetry distinguishes frequent queries, multi-page result sets, large
payloads, and throttling without changing SQL, indexing, throughput, or cleanup
behavior. It does not establish that a particular query is unindexed or that a
physical partition is saturated; use page diagnostics and Azure platform metrics
to verify those hypotheses.

## Attribution

The `call_site` label is a static, code-defined purpose, never a customer identifier.
The orphan cleanup controller supplies:

| Call site | Work attributed |
| --- | --- |
| `orphan_resource_inventory` | Listing subscription descendants in Resources |
| `orphan_desire_inventory` | Listing subscription desires in one MC container |
| `orphan_desire_client_lookup` | Resolving the per-MC client, including cold Fleet discovery |
| `orphan_resource_soft_delete` | Reading and replacing an orphaned Resources document |
| `orphan_desire_soft_delete` | Reading and replacing an orphaned desire document |

Other callers default to `unknown` but still receive source and query-shape
attribution. New call sites can use `cosmosmetrics.ContextWithCallSite` with a
static identifier. Never supply resource IDs, subscription IDs, names, SQL, or
query parameters. The query wrapper retains an explicit construction-time call
site through lazy iteration; page contexts supply current source identity,
cancellation, deadline, logger, and a fallback call site.

Every storage SQL pager is instrumented with one of these bounded shapes:

| Query shape | Meaning |
| --- | --- |
| `type_live` | Live resources of a type without a parent-prefix filter |
| `recursive_prefix_live` | Live descendants under a resource-ID prefix, all types |
| `typed_prefix_live` | Live descendants under a prefix filtered by type |
| `children_prefix_live` | Live direct children under a prefix, with path-depth filtering |
| `global_type_live` | Global live-resource list filtered by resource types |
| `global_active_operations` | Global nonterminal operations |
| `operations_active`, `operations_all` | Subscription operations, excluding or including terminal states |
| `missing_resource_id` | Documents missing a valid resource ID |
| `billing_all` | All billing documents |
| `billing_active` | Billing documents without deletion time |
| `billing_cluster_active` | Active billing documents for one cluster |
| `billing_cluster_active_ids` | IDs of active billing documents for patching |

Subscription operation shapes append `_request` when filtering by request, then
`_resource` or `_resource_tree` when filtering by external resource ID. These form
12 finite variants; parameter values never appear in labels. Global type lists
group their resource-type sets into one shape; the informer/source identifies
their purpose.

`query_scope` describes SDK routing: `single_partition` when a partition key is
supplied, otherwise `cross_partition`. It does not identify a partition key or
report the number of physical partitions visited. Nonquery calls have shape and
scope `none`, except SDK requests performed as part of an instrumented query.

## Query Metrics

All query metrics have labels `source_kind`, `source`, `cosmosdb_container`,
`call_site`, `query_shape`, and `query_scope`.

| Metric | Meaning |
| --- | --- |
| `cosmos_query_executions_total` | Pagers whose first page was requested, including failures |
| `cosmos_query_pages_total` | Page fetches after SDK retries, additionally labeled `outcome=success` or `error` |
| `cosmos_query_items_total` | Documents returned by successful pages |
| `cosmos_query_item_bytes_total` | Returned document JSON bytes, excluding response envelopes |
| `cosmos_query_empty_pages_total` | Successful empty pages, including empty continuation pages |
| `cosmos_query_page_duration_seconds` | Histogram of page-fetch latency, including SDK retries, for success and failure |

An unconsumed pager records no execution. A new pager using a continuation token
counts as a new execution, so `ListAll` and manually resumed lists can count more
than once per application-level list. An early-stopped pager counts once and only
records pages actually fetched. Returned items count even if later decoding or
application processing fails. Empty pages do not imply the whole query is empty.

RU accounting remains exclusively in the per-retry HTTP policy. The wrapper does
not add response charges again. Nil-response transport failures do not increment
the HTTP response counter, but a failed page increments the query page error
counter. Individual SDK retries are HTTP responses, not additional query pages.

## Diagnostic Logs

Each page emits `Cosmos DB query page` with attribution labels, a per-pager
`query_id`, one-based `page_index`, `activity_id`, outcome/status, returned item
count/bytes, latency, continuation presence, and the SDK's `query_metrics` string.
The SDK already requests these server query statistics; no index metrics are
enabled. Use retrieved/output document counts and sizes, index lookup time, and
document load time to investigate query execution cost.

Logs are at verbosity 4 for ordinary pages. Failed pages, pages whose final
response costs at least 100 RU, or page fetches taking at least one second are
logged at normal info level. This is threshold-based, not random sampling or a
global log-rate limit: sustained expensive queries can produce many records.

`final_response_request_units` is only the final response's charge, **not** a
retry-inclusive page total. A recovered 429 may appear only in the request
counters, not in an info-level log. `query_id` correlates pages of one pager, not
separately constructed continuation pagers or individual retry activity IDs.
Unavailable response status or charge is logged as null, not zero. In particular,
the SDK can discard response details after a decoding failure even though the HTTP
policy already counted its charge.

Telemetry does not add raw SQL, parameters, partition keys, item contents,
continuation tokens, session tokens, or full SDK error/diagnostic strings to logs.
Existing caller logger context, such as subscription and MC identity, is retained.
Use `activity_id` to correlate final page responses with Azure data-plane logs
where available.

## PromQL Examples

Add the appropriate environment/service filters. These examples deduplicate HA
scrapes before summing targets. Existing queries that aggregate away the new labels
continue working, but raw series and label-preserving recording rules gain those
dimensions.

RU/s by exact cleanup purpose and query shape:

```promql
sum by (cosmosdb_container, call_site, query_shape, query_scope, operation) (
  max without (prometheus_replica) (
    rate(cosmos_request_units_total{
      source_kind="controller", source="DeleteOrphanedCosmosResources"
    }[5m])
  )
)
```

Average SQL RU per started pager, across all pages and charged retries in the
window. This is a windowed ratio, not a histogram of complete logical calls:

```promql
sum by (source, cosmosdb_container, call_site, query_shape, query_scope) (
  max without (prometheus_replica) (
    rate(cosmos_request_units_total{operation="query"}[15m])
  )
)
/
sum by (source, cosmosdb_container, call_site, query_shape, query_scope) (
  max without (prometheus_replica) (
    rate(cosmos_query_executions_total[15m])
  )
)
```

Throttled HTTP responses/s by purpose, including retries that eventually succeed:

```promql
sum by (source, cosmosdb_container, call_site, query_shape) (
  max without (prometheus_replica) (
    rate(cosmos_requests_total{status_code="429"}[5m])
  )
)
```

Compare execution rate, successful pages per execution, returned bytes per page,
and empty-page rate alongside these panels. Do not interpret HTTP response count
as query count, or zero-RU 429s as an absence of throttling.
