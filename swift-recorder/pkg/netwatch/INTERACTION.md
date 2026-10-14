# netwatch Interaction Diagram

`Run` owns the monitor and tracked link state on one goroutine. It calls
`emit` synchronously. There is no separate notification-reader goroutine,
userspace event channel, or address-state collector.

```mermaid
sequenceDiagram
    participant Caller
    participant Runner as runner / generation
    participant Monitor as subscribed link socket
    participant Dump as link dump socket
    participant Emit as emit(Record)

    Caller->>Runner: Run(ctx, emit, opts)
    loop one attempt per generation
        Runner->>Monitor: open and subscribe to RTNLGRP_LINK
        Runner->>Dump: bounded filtered RTM_GETLINK dump
        Dump-->>Runner: selected link baseline or error
        Runner->>Monitor: Drain queued notifications without blocking
        Monitor-->>Runner: events in receive order or error
        Runner->>Runner: replay onto baseline without change observations
        Runner->>Emit: periodic record, status.available=true

        loop until cancellation or generation failure
            Runner->>Monitor: Recv (bounded receive timeout)
            Monitor-->>Runner: link events or error
            Runner->>Runner: apply events to selected link state
            opt tracked link changes
                Runner->>Emit: change record with observations and resulting state
            end
            opt two-minute tick
                Runner->>Emit: periodic state record
            end
        end

        Runner->>Monitor: Close
        alt cancellation
            Runner-->>Caller: ctx.Err()
        else dump / notification / subscription failure
            Runner->>Emit: periodic record, status.available=false
            Runner->>Runner: discard generation and wait bounded backoff
            Note over Runner,Emit: Unavailable periodic records continue during backoff
        end
    end
```

The subscription is opened before the baseline dump. Notifications accumulate
in its kernel socket buffer while the separate dump socket is read. `Drain`
then receives queued notifications until the socket would block. Replaying
these events reconstructs current selected state; it does not establish which
changes occurred after the dump, so replay emits no change observations.

Kernel overflow, malformed relevant notifications, dump failure, and
subscription failure invalidate the generation. The runner emits unavailable
status without stale state, then opens a new subscription and rebuilds from
scratch. Backoff doubles to a cap and resets after a generation has reached
available state. The periodic ticker is serviced between receives and during
retry backoff; collection and replay are synchronous.

The emitter's session ID and record sequence persist across generations.
`last_applied_notification` is generation-local and resets on reconstruction;
it is not a kernel sequence number or proof of lossless event history.
