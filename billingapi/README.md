# Shared billing document contract

This module defines the billing document shared by the ARO-HCP resource provider
and HCP Billing. See [types.go](types.go) for the schema and field ownership.

Each service keeps its storage metadata and processing state in a local wrapper;
the RP also keeps its durable orphan candidacy there.
The persisted JSON remains flat. Resource IDs use the Azure SDK's parsed type
in Go and serialize as strings.

Missing or null resource IDs remain nil; malformed non-null IDs fail decoding.
Consumers must handle those cases before processing a document. Parsing validates
the ARM path structure, not resource existence or billing eligibility.

## Billing timestamps

The RP supplies the lifecycle timestamps; Billing records its processing progress:

- `creationTime` initializes missing billing watermarks and starts hosting billing.
  The RP creates the document after provisioning succeeds, but currently copies
  the cluster's `SystemData.CreatedAt`, falling back to the current time if absent.
  It is not a dedicated provisioning-completion timestamp.
- `deletionTime` ends hosting billing and contributes to billing-document cleanup
  eligibility. The RP normally sets it near the end of cluster cleanup; orphan
  recovery uses the time candidacy is confirmed after its grace period. It is not
  the time the customer requested deletion and does not directly cap VM usage events.
- Billing-owned watermarks track processing progress. Missing values remain nil
  in the shared type; Billing initializes them when processing a document.

## Updating the contract

Patch only fields your service owns so other state and metadata are preserved.
Keep schema changes compatible with independently deployed readers and writers;
coordinate changes to timestamp meanings across both services.
