# Anonymous registry failure diagnostics

`CaptureRegistryFailureDiagnostics` adds test-runner observations after image
verification fails. Success makes no diagnostic requests. The caller still
asserts its original error, with its original assertion message; collection and
artifact-write errors do not add a failure or replace that error.

The helper makes at most three anonymous HTTPS GETs, without retries:

1. `/v2/` at the registry in the test's full image reference.
2. If needed, the target manifest endpoint to discover a Bearer challenge and
   repository pull scope. Multiple challenges and quoted parameters are parsed.
3. The advertised Bearer realm, forwarding only validated service and target
   repository pull scope parameters. Service identifiers may contain underscores.
   Basic/unsupported or missing challenges do not trigger a token request.

Requests use ordinary DNS resolution and HTTP transport with verified TLS.
There is no destination IP policy or per-registry configuration. Redirects and
proxies are disabled. URLs with embedded credentials, queries or fragments are
rejected before adding protocol parameters to a token request. The helper does
not read authentication files or secrets, send credentials or cookies, use a
returned token, or read response bodies. It creates no pods or image downloads.

Collection has an independent 30-second context, including after the test
context ends. Dialing, TLS handshakes and response headers each have a five-second
limit; dialing also respects the collection deadline. Response headers are
bounded to 16 KiB, and response bodies are closed unread.

## Artifact and interpretation

`pod-diagnostics/<namespace>/<pod>/registry.json` sits beside the unchanged
`pod.yaml` and `events.yaml`. It records UTC request start times, test-runner
origin, DNS/connect/TLS/request durations, HTTP statuses and categorized outcomes.
`connect_ms` sums completed connection-attempt durations paired by network and
address, including failed attempts, as observed before the request snapshot.
It is not wall-clock elapsed time when attempts overlap. Unmatched completion
callbacks and attempts still in progress at the snapshot contribute nothing.
Raw URLs, headers, error text, tokens and response bodies are not stored.
Console output is one artifact pointer or a short collection-unavailable notice.

`root_cause` is always `undetermined`. A 200 is not pull proof; a 401 on `/v2/`
commonly means authentication is required. Anonymous 401/403 responses say
nothing about the test's credentials. A 5xx, transport error or probe deadline
is an observation at collection time, not proof of a registry-wide outage or the
node's experience. Unqualified image names, non-SHA256 digests and authentication
protocols outside the supported Bearer parameters are not supported. Timings
reflect observed client trace callbacks, not a complete network diagnosis.

## Validation

Tests use injected transports and local TLS servers, not external registries.
Run with the repository-pinned tooling:

```sh
go test ./test/util/verifiers
make lint MODULES='./test/util/verifiers ./test/e2e' LINT_GOTAGS=E2Etests
make -C test aro-hcp-tests
make verify-gomega-assertions verify-gomega-cmpdiff verify-mi-containers
```
