# Background Knowledge

## Actors

1. REST Client - typically `az`.  Can be anything able to make the HTTP request.
2. ARO-HCP/frontend - receives REST Client requests via External API, converts to Internal API, validates, converts to Cosmos API, and writes.
3. ARO-HCP/backend - reads the Cosmos API, converts to Internal API, makes changes, converts to Cosmos API and then writes.

## Categories of APIs

1. External API descriptions - these are typescript files stored somewhere in this repo
2. External API - these are generated in `internal/api/<version>/generated` package.
   They are generated from that typescript.  The server uses these for marshalling/unmarhsalling
3. Internal API - these are handwritten an located in `internal/api` package (may move that).
   They are used inside the frontend.
   The server uses these for validation and logic.
4. Cluster-Service API - these are `github.com/openshift-online/ocm-api-model`.
   They are used for cluster-service.
5. Cosmos API - these are handwritten in `internal/database`.
   They are stored in cosmos and make use of the Internal API for formatting.
   They are used inside frontend and backend.

On a REST Client read, the frontend will go through Cosmos API -> Internal API -> External API.

## Major Packages

1. validation - holds all static validation rules written against internal APIs.
   The only information available is the new and old internal API instance.
   Validation only runs inside of the frontend apiserver, NOT on the backing cosmos storage.
   This means the backend can directly modify content is ways that bypass all validation rules.
2. admission - holds dynamic validation rules written against internal APIs.
   These can include information from the wider state of the world.
   For instance, node validation requires a cluster instance.
   However, remember that barring perfection, the state of the world can drift from an initially valid state.
   For instance, just becase a NodePool version matches the control plane now, the control plane could change version in the future, making it invalid.
3. conversion - holds information about moving in and out of internal APIs.
   Currently only used to go to/from cosmos APIs
4. database - holds clients and types for interacting with data stored in persistent storage.

## Conversion
To keep frontend and backend logic reasonable, a single version of the API is used: the Internal API.
There are many External APIs (REST Client) so that customers can write logic once and never update it and there is a single
Internal API version that acts as a hub for conversion to and from External APIs.
We write our validation and frontend/backend logic against the Internal API which may evolve over time without notice.

## Testing

### Conversion Testing
When testing conversions, it is critical to create fuzz tests that create randomly filled values of an original, then transform
to an intermediate, then transform back and compare the result for sameness.

**Critical limitation!**  Our current state is such that the External API can represent more states than the Internal API.
This means we can reliably go internal-external-internal, but *cannot* go external-internal-external.
While we may someday fix this, the existing debt load is such that we'll have to accept the limitation and deal with the issue later.

The fuzz testing lives with the External API types in the `internal/api/<version>` package.
The fuzz testing for the Cosmos API types lives in `internal/database`.