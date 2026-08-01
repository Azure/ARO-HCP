# node-health detectors

This package is the pure detection core of the node-health controller. It answers
one question, with no Kubernetes I/O: given a node and the Events and Pods
currently held for it, is the node **wedged**, **healthy** (recovered), or is the
evidence insufficient to say? The controller package (`..`) consumes the verdict
and is the only thing that talks to the API server (labeling, events, metrics).

Keeping detection isolated and side-effect free is what makes it exhaustively
table-testable: every rule below is exercised by `decide_test.go` against
synthetic Events and Pods, no cluster required.

## The decision

`Decide(node, events, pods, now, observedSince, lastSuccessAt) (Decision, Snapshot)`
returns one of:

- `DecisionWedged`: a detector fired. The controller ensures the node carries the
  wedged label.
- `DecisionHealthy`: recovery is positively observed (a recorded per-node success
  falls within the window). The controller ensures the node is not labeled.
- `DecisionUnknown`: the evidence is insufficient, so leave the label exactly as it
  is. This covers a `NotReady` node (deferred to node lifecycle) and the cold view
  before a full window has been observed since observation began
  (`observedSince`). Because `Unknown` never removes a label, an existing wedged
  label survives a restart until recovery is actually seen, and a cold view never
  invents a fresh wedge.
- `DecisionNotApplicable`: no detector applies to the node, so none can ever mark
  it wedged. The controller ensures the node is not labeled. This is a definitive
  statement about ownership, unlike `Unknown` which is a statement about evidence,
  and it is what retires a label left on a node that has since stopped being a
  detection candidate. Applicability is evaluated ahead of the `NotReady` check,
  because a node no detector owns is not ours to hold a label on whether it is
  Ready or not.

`Decide` short-circuits on a wedge, only reports `Healthy` on positive success
evidence, and otherwise stays `Unknown`. It is a pure function of the node's
observed state and the controller's recorded per-node success history: same
inputs, same output.

## How a detector is structured

A **detector** is one fault family. Detection is modular so that adding a family
is adding code in its own file, never editing the engine:

- **`decide.go`**: the engine. It defines the `Detector` interface
  (`Applies`, `Evaluate`, `Fires`, plus `Name`/`Reason`), the `Snapshot` of
  evaluated evidence, the `Decision` type, the `registry` of detectors, and
  `Decide`, which iterates the registry without knowing any concrete type.
- **`signature_detector.go`**: the shared base. `signatureDetector` implements
  the `Detector` interface once for the common shape and carries the reusable
  toolkit (windowed correlation of failure Events to stuck pods, condition-based
  dwell math, node-Ready and success helpers). Families that fit this shape reuse
  all of it.
- **`swift_vf.go`**: one fault family's specifics only, the `swift-vf-teardown`
  detector value plus its applicability predicate. No evaluation logic lives here.

`Decide` walks `registry`. For each detector it calls `Applies(node)` (skip if the
node can't exhibit the fault), then `Evaluate(...)` to gather the evidence
`Snapshot`, then `Fires(snap, now, observedSince)`. The first detector that fires
wins and the node is `Wedged`; if none fires but some success was seen, the node is
`Healthy`; otherwise `Unknown`.

## The `signatureDetector` shape

Most kubelet-visible wedges look the same: pods that cannot get a sandbox, marked
by a particular Event `reason` whose message matches a signature, with no fresh
sandbox coming up. The base captures that shape with these knobs (all code
constants, never config):

| field                | meaning |
|----------------------|---------|
| `appliesTo`          | predicate limiting the detector to nodes that can physically exhibit the fault (nil = all nodes) |
| `eventReason`        | the kubelet Event `reason` that marks a pod as failing sandbox creation |
| `signatures`         | regexes matched against the failure Event message; any match is a hit |
| `failuresFloor`      | minimum number of stuck pods that have each individually been stuck past `dwell` (a floor so the storm is sustained, not the trigger) |
| `window`             | rolling window over which failures and successes are counted |
| `dwell`              | how long each pod must have been stuck before it counts toward the floor, filtering transient bursts |
| `requireZeroSuccess` | when true, firing requires zero fresh sandbox successes in the window |

`Fires` is: `SustainedCount >= failuresFloor` (stuck pods that have each been stuck
at least `dwell`) **and** (`RecentSuccess == false` when `requireZeroSuccess`, and
only once a full `window` has elapsed since `observedSince`).

Three details that matter, all of them chosen so no signal depends on Events or
Pods that the apiserver garbage-collects:

- **The floor and dwell are read from live Pod state, not from Event counts.** A
  "stuck pod" is a pod whose `PodReadyToStartContainers` condition is `False` and
  which is the subject of a matching failure Event in the window. Dwell is measured
  per pod, from each pod's own `PodReadyToStartContainers=False` `lastTransitionTime`,
  and `SustainedCount` counts the stuck pods whose own dwell has elapsed, so the
  floor cannot be met by one long-stuck pod plus a burst of brand-new ones. Both are
  durable across Event GC and a controller restart. `FailureCount` (all stuck pods)
  and `StuckSince` (the oldest) are retained for reporting only. Events are used
  only to classify which pods are failing (looked up per node by `Event.Source.Host`,
  which is deletion-safe, and correlated to a specific Pod by `InvolvedObject.UID` so
  a recycled name cannot inherit a deleted Pod's Events); they are never counted.
- **Success presence is load-bearing, and it is a recorded history, not a scan.**
  `requireZeroSuccess` is what separates a hard wedge (VF gone, no fresh sandboxes)
  from a flap (some pods still starting). A success is a non-host-network pod whose
  `PodReadyToStartContainers` condition transitioned to `True`; host-network pods
  are excluded, since they reach the condition without a delegated NIC. The
  controller records the most recent such transition per node (`SuccessAt` feeds a
  bounded `lastSuccessAt`), advanced from pod add/update **and** the last-seen state
  of a deleted pod, so a short-lived success survives its Pod being garbage
  collected. `Decide` sets `RecentSuccess` when `lastSuccessAt` falls inside the
  window; it never scans the passed-in Pods for success.
- **No recorded success is `Unknown`, never a wedge, until the controller has
  watched a full window.** Just after a restart or a disabled→enabled transition
  the recorded history starts empty, so a detector does not fire until
  `now - observedSince >= window`; before that, an empty success history is treated
  as indeterminate, not as proof of a wedge.

## The one detector today: `swift-vf-teardown`

Applies only to SWIFT-v2 delegated-NIC nodes (label
`kubernetes.azure.com/podnetwork-swiftv2-enabled=true`, exported as
`SwiftV2LabelKey`/`SwiftV2LabelValue`): a node with no delegated secondary NIC
cannot suffer a VF teardown, so it is never a candidate. On those nodes it matches
`FailedCreatePodSandBox` Events in the `route ip+net: no such network interface`
family (plus the `network is unreachable`, `mtpnc is not ready`, and
`dhcp discover ... timed out` variants), with `failuresFloor: 3`, `window: 10m`,
`dwell: 10m`, `requireZeroSuccess: true`.

`SwiftV2LabelKey`/`Value` are exported because the controller uses them to scope
its periodic sweep to the nodes a detector can actually fire on, rather than
sweeping every node in the cluster. The sweep is the union of that selector and
the wedged-label selector, so a node that stops being a detection candidate while
labeled is still swept and has its stale label retired
(`DecisionNotApplicable`), including when that transition happened while the
controller was down.

## Adding a fault family

1. If it fits the signature shape, add a new `signatureDetector` value in its own
   file (e.g. `myfault.go`) with its constants, plus its `appliesTo` predicate.
2. If it needs different evidence (for example reading a CRD, not Events), add a
   new type in its own file implementing the `Detector` interface directly.
3. Register it in `registry` in `decide.go`.
4. Add table cases to `decide_test.go`.

`Decide` needs no changes in any case: it only knows the interface.
