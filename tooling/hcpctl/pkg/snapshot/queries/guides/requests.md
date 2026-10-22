# Requests

ARM requests are identified by two IDs, both logged by the frontend:

- **CorrelationID** (`log.correlation_request_id`): set by ARM, shared by every request in
  one client operation. Objectives often quote it.
- **ClientRequestID** (`log.client_request_id`): identifies one request.

`frontend/frontendRequests` lists every request against a resource group with both IDs,
the method, the resource path, and the status. Pick the requests that matter (for example,
the failing PUT or the DELETE), and pass their IDs explicitly.

## Chain

1. `frontend/clientRequestId` turns a CorrelationID into a ClientRequestID.
2. `frontend/requestLogs` shows every frontend log line of the ClientRequestID.
3. A mutating request (PUT, PATCH, DELETE, POST) returns an async operation. Run
   `frontend/asyncOperationPath` (and `frontend/asyncOperationId`) with the
   ClientRequestID.
4. `frontend/asyncOperationRequests` shows ARM polling the operation status, and thus how
   and when the operation finished.

When a step returns several values (for example, a correlation ID that spans several
requests), nothing is remembered. Pick the right value and pass it explicitly.
