# Verification

[Documentation index](README.md)

## Existing E2E test

The [Shoebox E2E test](../../test/e2e/shoebox_logs.go#L226) creates an HCP cluster and customer destinations.
It creates Storage and Event Hub diagnostic settings.
It then waits for both destination verifiers.

The [category list](../../test/e2e/shoebox_logs.go#L43) contains nine categories.
It excludes `cluster-autoscaler` and `capi-provider`.
The [Lua mapping](record-format.md#category-mapping) supports eleven categories.

## Storage verifier

[verifyShoeboxLogsImpl.Verify](../../test/util/verifiers/shoebox.go#L54) lists Storage containers.
The verifier succeeds when at least one container name starts with `insights-logs-`.

It does not check:

- Blob contents.
- Record timestamps.
- Customer resource identity.
- Delivery for each category.

## Event Hub verifier

[verifyShoeboxEventHubImpl.Verify](../../test/util/verifiers/shoebox_eventhub.go#L56) reads Event Hub partitions.
The receiver starts from the earliest available events.
It reads a JSON envelope with a `records` array.

[isValidShoeboxRecord](../../test/util/verifiers/shoebox_eventhub.go#L147) requires these fields to contain values:

- `time`.
- `resourceId`.
- `category`.
- `level`.
- `operationName`.
- `location`.

It also requires non-null `properties`.
The verifier needs at least one accepted record.

The predicate does not compare the resource ID with the expected HCP resource.
It does not check timestamp age, every category, or exact property contents.
Partition read errors can also cause verification failure.

## Coverage limits

| Check | Existing evidence | Additional evidence required |
|---|---|---|
| Storage delivery | A matching container exists | Inspect actual records |
| Event Hub delivery | A record has required fields | Check expected values and time |
| All categories | Nine categories in test settings | Verify each required category |
| Log Analytics | No check in this test | Separate destination verification |
| Delivery guarantee | No proof in these tests | A separately defined service contract |

## Acceptance procedure for a change

1. List the categories affected by the change.
2. Verify the generated configuration for those categories.
3. Enable the categories on a test HCP resource.
4. Record the start time of the test.
5. Produce logs from each affected component.
6. Inspect records at each required destination.
7. Compare resource IDs with the test HCP resource ID.
8. Check that record timestamps belong to the test interval.
9. Check each category and its properties.
10. Record the tested source revision and configuration version.

A successful existing verifier is useful evidence of basic delivery.
It does not replace the field and category checks above.

Use the [repository test guidance](../../test/AGENTS.md) for test execution requirements.
