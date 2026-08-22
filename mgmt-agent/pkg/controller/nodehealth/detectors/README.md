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

`Decide(node, events, pods, now) (Decision, Snapshot)`
returns one of:

- `DecisionWedged`: a detector fired. The controller ensures the node carries the
  wedged label.
- `DecisionHealthy`: recovery is positively observed (a pod on the node got a
  sandbox inside the window). The controller ensures the node is not labeled.
- `DecisionUnknown`: the evidence is insufficient, so leave the label exactly as it
  is. This covers a `NotReady` node (deferred to node lifecycle) and a node that is
  simply quiet: no storm and no success either. Because `Unknown` never removes a
  label, an existing wedged label survives a restart until recovery is actually
  seen.
- `DecisionNotApplicable`: no detector applies to the node, so none can ever mark
  it wedged. The controller ensures the node is not labeled. This is a definitive
  statement about ownership, unlike `Unknown` which is a statement about evidence,
  and it is what retires a label left on a node that has since stopped being a
  detection candidate. Applicability is evaluated ahead of the `NotReady` check,
  because a node no detector owns is not ours to hold a label on whether it is
  Ready or not.

`Decide` short-circuits on a wedge, only reports `Healthy` on positive success
evidence, and otherwise stays `Unknown`. It is a pure function of the node and the
Events and Pods held for it: same inputs, same output, and nothing is carried
between calls. Every input is something a `LIST` returns, so a controller that has
just started decides exactly what one running for hours would, with no warm-up.

## How a detector is structured

A **detector** is one fault family. Detection is modular so that adding a family
is adding code in its own file, never editing the engine:

- **`decide.go`**: the engine. It defines the `Detector` interface
  (`Applies`, `Evaluate`, `MeetsThreshold`, plus `Name`/`Reason`), the `Snapshot` of
  evaluated evidence, the `Decision` type, the `registry` of detectors, and
  `Decide`, which iterates the registry without knowing any concrete type.
- **`signature_detector.go`**: the shared base. `signatureDetector` implements
  the `Detector` interface once for the common shape and carries the reusable
  toolkit (windowed correlation of failure Events to stuck pods, condition-based
  dwell math, node-Ready and success helpers). Families that fit this shape reuse
  all of it.
- **`swift_vf.go`** and **`cni_plugin_not_initialized.go`**: each fault family's
  specifics only. No evaluation logic lives in these files.

`Decide` walks `registry`. For each detector it calls `Applies(node)` (skip if the
node can't exhibit the fault), then `Evaluate(...)` to gather the evidence
`Snapshot`, then `MeetsThreshold(snap, now)`. The first detector that fires
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
| `signatures`         | regexes matched against the failure Event message; any match is a hit. The first one that matches classifies the pod, and the signature classifying most of the counted pods is reported in the `Snapshot` for triage (ties broken by declaration order, so it is stable) |
| `failuresFloor`      | minimum number of stuck pods that have each individually been stuck past `dwell` (a floor so the storm is sustained, not the trigger) |
| `window`             | rolling window over which failures and successes are counted |
| `dwell`              | how long each pod must have been stuck before it counts toward the floor, filtering transient bursts |
| `requireZeroSuccess` | when true, firing requires zero fresh sandbox successes in the window |

`MeetsThreshold` is: `SustainedCount >= failuresFloor` (stuck pods that have each been stuck
at least `dwell`) **and** `RecentSuccess == false` when `requireZeroSuccess`.

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
- **Success presence is load-bearing, and it is read from the same Pods.**
  `requireZeroSuccess` is what separates a hard wedge (VF gone, no fresh sandboxes)
  from a flap (some pods still starting). `Evaluate` scans the node's Pods with
  `SuccessAt` and sets `RecentSuccess` when one lands inside the window. Two shapes
  count, and host-network pods are excluded from both since they reach a running
  state without a delegated NIC:
  - a live pod whose `PodReadyToStartContainers` condition transitioned to `True`,
    timed by that transition. Keying on the transition rather than pod age means a
    container restarting inside an existing sandbox is not mistaken for success:
    the sandbox is what needs the network.
  - a pod that reached a terminal phase with a container that ran exactly once,
    timed by that container's start. A finished pod drops the condition back to
    `False` as a matter of course, so without this a node whose recent traffic was
    short-lived Job and CronJob pods would read as having had no success at all.
    The restart count matters: a terminated state describes the container's latest
    run, so on a restarted container the start time belongs to a run inside the
    sandbox the pod already had, which proves nothing. Those containers are not
    evidence in either direction.
  The window comparison is absolute, so a timestamp dated ahead by kubelet clock
  skew is measured by its distance from now rather than its sign. Skew inside the
  window still counts as a success, which is harmless, and anything further out is
  discarded rather than read as an arbitrarily fresh success.
- **Nothing is remembered between reconciles.** Both signals come out of the
  informer caches, which is the whole point: the only state the controller keeps is
  state a `LIST` can rebuild. There is no success map to lose on restart and so no
  warm-up window during which a genuinely wedged node goes unreported. The
  trade-off is deliberate: a success is only visible while its Pod is still in the
  cache, which is why the terminal-pod shape above is counted rather than relying on
  a pod still being alive at reconcile time.

## Detectors

Both current detectors apply only to SWIFT-v2 delegated-NIC nodes (label
`kubernetes.azure.com/podnetwork-swiftv2-enabled=true`, exported as
`SwiftV2LabelKey`/`SwiftV2LabelValue`): a node with no delegated secondary NIC
is outside the observed failure scope.

- `swift-vf-teardown` matches `FailedCreatePodSandBox` Events in the
  `route ip+net: no such network interface` family, plus the
  `network is unreachable`, `mtpnc is not ready`, and
  `dhcp discover ... timed out` variants.
- `cni-plugin-not-initialized` matches `NetworkNotReady` Events carrying
  `NetworkPluginNotReady: cni plugin not initialized`. In this modality the node
  remains `Ready`, but its CNI never initializes and newly scheduled pods cannot
  get sandboxes.

Both use `failuresFloor: 3`, `window: 10m`, `dwell: 10m`, and
`requireZeroSuccess: true`.

`SwiftV2LabelKey`/`Value` are exported because the controller uses them to scope
its periodic sweep to the nodes these detectors can actually fire on, rather than
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
